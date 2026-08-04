package ecs

import (
	"testing"
	"time"
)

type recorder struct{ seq []int }

type markSystem struct {
	order int
	r     *recorder
}

func (s *markSystem) Update(w *World, dt time.Duration) {
	s.r.seq = append(s.r.seq, s.order)
}

func TestSystemOrder(t *testing.T) {
	w := NewWorld()
	r := &recorder{}
	w.AddSystem(20, &markSystem{order: 20, r: r})
	w.AddSystem(5, &markSystem{order: 5, r: r})
	w.AddSystem(10, &markSystem{order: 10, r: r})
	w.RunSystems(100 * time.Millisecond)
	want := []int{5, 10, 20}
	if len(r.seq) != 3 {
		t.Fatalf("seq = %v", r.seq)
	}
	for i := range want {
		if r.seq[i] != want[i] {
			t.Fatalf("system order = %v, want %v", r.seq, want)
		}
	}
}

func TestDuplicateSystemOrderPanics(t *testing.T) {
	w := NewWorld()
	r := &recorder{}
	w.AddSystem(10, &markSystem{order: 10, r: r})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate order")
		}
	}()
	w.AddSystem(10, &markSystem{order: 10, r: r})
}
