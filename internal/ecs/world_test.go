package ecs

import "testing"

type hp struct{ Cur, Max int }

func TestWorldCreateDestroyEntity(t *testing.T) {
	w := NewWorld()
	a := w.CreateEntity()
	b := w.CreateEntity()
	if a != 1 || b != 2 {
		t.Fatalf("dense allocation: got %d, %d", a, b)
	}
	if !w.IsAlive(a) || !w.IsAlive(b) || w.EntityCount() != 2 {
		t.Fatal("alive bookkeeping wrong")
	}
	w.DestroyEntity(a)
	if w.IsAlive(a) || w.EntityCount() != 1 {
		t.Fatal("destroy failed")
	}
	c := w.CreateEntity()
	if c != a {
		t.Fatalf("expected free-list reuse of %d, got %d", a, c)
	}
}

func TestWorldDestroyTwicePanics(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	w.DestroyEntity(e)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on double destroy")
		}
	}()
	w.DestroyEntity(e)
}

func TestWorldAddGetHasRemove(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	Add(w, e, hp{Cur: 80, Max: 100})
	if !Has[hp](w, e) {
		t.Fatal("Has false after Add")
	}
	p := Get[hp](w, e)
	p.Cur = 70 // 指针直改
	if Get[hp](w, e).Cur != 70 {
		t.Fatal("Get pointer not writing to storage")
	}
	Set(w, e, hp{Cur: 60, Max: 100})
	if Get[hp](w, e).Cur != 60 {
		t.Fatal("Set failed")
	}
	Remove[hp](w, e)
	if Has[hp](w, e) {
		t.Fatal("Has true after Remove")
	}
	Remove[hp](w, e) // 幂等
}

func TestWorldOpsOnDeadEntityPanic(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	w.DestroyEntity(e)
	for _, f := range []func(){
		func() { Add[hp](w, e, hp{}) },
		func() { Set[hp](w, e, hp{}) },
		func() { Get[hp](w, e) },
		func() { Remove[hp](w, e) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic for dead entity op")
				}
			}()
			f()
		}()
	}
	if Has[hp](w, e) {
		t.Fatal("Has on dead entity should be false")
	}
}

func TestWorldDestroyEntityRemovesComponents(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	Add(w, e, hp{})
	Add(w, e, pos{})
	w.DestroyEntity(e)
	if s := storage[hp](w); s.Len() != 0 {
		t.Fatal("hp storage not cleaned")
	}
	if s := storage[pos](w); s.Len() != 0 {
		t.Fatal("pos storage not cleaned")
	}
}

func TestWorldResources(t *testing.T) {
	w := NewWorld()
	seed := uint64(7)
	w.AddResource(&seed)
	if Resource[uint64](w) == nil {
		t.Fatal("resource nil")
	}
	*Resource[uint64](w) = 99
	if *Resource[uint64](w) != 99 {
		t.Fatal("resource mutation failed")
	}
}

func TestWorldAddResourcePanics(t *testing.T) {
	w := NewWorld()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	w.AddResource(uint64(1)) // 传值而非指针
}

func TestWorldDuplicateResourcePanics(t *testing.T) {
	w := NewWorld()
	a, b := uint64(1), uint64(2)
	w.AddResource(&a)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate resource")
		}
	}()
	w.AddResource(&b)
}
