package ecs

import "testing"

type pos struct{ X, Y int }

func TestSparseSetAddGetHas(t *testing.T) {
	s := newSparseSet[pos]()
	s.Add(3, pos{X: 1, Y: 2})
	s.Add(7, pos{X: 3, Y: 4})

	if !s.Has(3) || !s.Has(7) {
		t.Fatal("expected entities present")
	}
	if s.Has(1) {
		t.Fatal("entity 1 should not have component")
	}
	p := s.Get(7)
	if p.X != 3 || p.Y != 4 {
		t.Fatalf("Get = %+v", *p)
	}
	p.X = 99 // 指针直接修改
	if s.Get(7).X != 99 {
		t.Fatal("pointer mutation did not affect storage")
	}
}

func TestSparseSetDuplicateAddPanics(t *testing.T) {
	s := newSparseSet[pos]()
	s.Add(1, pos{})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate Add")
		}
	}()
	s.Add(1, pos{})
}

func TestSparseSetGetMissingPanics(t *testing.T) {
	s := newSparseSet[pos]()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on Get missing")
		}
	}()
	s.Get(42)
}

func TestSparseSetRemoveSwapDelete(t *testing.T) {
	s := newSparseSet[pos]()
	for _, e := range []Entity{1, 2, 3, 4, 5} {
		s.Add(e, pos{X: int(e)})
	}
	// 移除中间元素 2：交换删除把最后一个（5）挪过来
	s.Remove(2)

	if s.Has(2) {
		t.Fatal("entity 2 should be removed")
	}
	for _, e := range []Entity{1, 3, 4, 5} {
		if p := s.Get(e); p.X != int(e) {
			t.Fatalf("entity %d: got %+v", e, *p)
		}
	}
	// dense 顺序：1,5,3,4（2 被 5 顶替）
	want := []int{1, 5, 3, 4}
	if len(s.dense) != len(want) {
		t.Fatalf("dense len = %d", len(s.dense))
	}
	for i, x := range want {
		if s.dense[i].X != x || int(s.entities[i]) != x {
			t.Fatalf("dense[%d] = %+v entity %d", i, s.dense[i], s.entities[i])
		}
	}
}

func TestSparseSetRemoveIdempotent(t *testing.T) {
	s := newSparseSet[pos]()
	s.Add(1, pos{})
	s.Remove(1)
	s.Remove(1) // 不 panic
	if s.Len() != 0 {
		t.Fatal("expected empty")
	}
}

func TestSparseSetIterationDeterministic(t *testing.T) {
	run := func() []Entity {
		s := newSparseSet[pos]()
		for _, e := range []Entity{5, 2, 8, 1} {
			s.Add(e, pos{})
		}
		s.Remove(2)
		out := make([]Entity, 0, len(s.entities))
		for _, e := range s.entities {
			out = append(out, e)
		}
		return out
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatal("length mismatch")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("iteration not deterministic: %v vs %v", a, b)
		}
	}
}
