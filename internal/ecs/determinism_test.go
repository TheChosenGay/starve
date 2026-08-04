package ecs

import (
	"maps"
	"slices"
	"testing"
	"time"
)

type rng struct{ seed uint64 }

func (r *rng) Next() uint64 {
	r.seed = r.seed*6364136223846793005 + 1442695040888963407
	return r.seed
}

type wanderSystem struct{}

func (s *wanderSystem) Update(w *World, dt time.Duration) {
	r := Resource[rng](w)
	Query[pos](w, func(e Entity, p *pos) {
		p.X += int(r.Next() % 3)
		p.Y += int(r.Next() % 2)
	})
}

func runReplay() (positions map[Entity]pos, seed uint64, events []Event, dirty []DirtyEntry) {
	w := NewWorld()
	w.AddResource(&rng{seed: 42})
	w.AddSystem(10, &wanderSystem{})

	e1 := w.CreateEntity()
	e2 := w.CreateEntity()
	Add(w, e1, pos{X: 1, Y: 1})
	Add(w, e2, pos{X: 2, Y: 2})
	Add(w, e1, hp{Cur: 10, Max: 10})

	w.RunSystems(100 * time.Millisecond)
	Set(w, e2, pos{X: 9, Y: 9})
	w.RunSystems(100 * time.Millisecond)
	Remove[hp](w, e1)
	w.DestroyEntity(e2)

	positions = make(map[Entity]pos)
	Query[pos](w, func(e Entity, p *pos) { positions[e] = *p })
	seed = Resource[rng](w).seed
	events = w.DrainEvents()
	dirty = w.DrainDirtySorted()
	return positions, seed, events, dirty
}

func TestDeterministicReplay(t *testing.T) {
	p1, s1, ev1, d1 := runReplay()
	p2, s2, ev2, d2 := runReplay()
	if !maps.Equal(p1, p2) {
		t.Fatalf("positions differ: %v vs %v", p1, p2)
	}
	if s1 != s2 {
		t.Fatalf("rng seed differ: %d vs %d", s1, s2)
	}
	if len(ev1) != len(ev2) {
		t.Fatalf("event count differ: %d vs %d", len(ev1), len(ev2))
	}
	for i := range ev1 {
		if ev1[i] != ev2[i] {
			t.Fatalf("events differ at %d: %+v vs %+v", i, ev1[i], ev2[i])
		}
	}
	if len(d1) != len(d2) {
		t.Fatalf("dirty count differ: %d vs %d", len(d1), len(d2))
	}
	for i := range d1 {
		if d1[i].Entity != d2[i].Entity || !slices.Equal(d1[i].Comps, d2[i].Comps) {
			t.Fatalf("dirty differ at %d: %+v vs %+v", i, d1[i], d2[i])
		}
	}
}
