package ecs

import "testing"

func BenchmarkSparseSetAdd(b *testing.B) {
	s := newSparseSet[pos]()
	ents := make([]Entity, 10000)
	for i := range ents {
		ents[i] = Entity(i + 1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := ents[i%len(ents)]
		s.Remove(e)
		s.Add(e, pos{X: i, Y: i})
	}
}

func BenchmarkSparseSetGet(b *testing.B) {
	s := newSparseSet[pos]()
	ents := make([]Entity, 10000)
	for i := range ents {
		ents[i] = Entity(i + 1)
		s.Add(ents[i], pos{X: i})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = s.Get(ents[i%len(ents)])
	}
}

func BenchmarkWorldQuery(b *testing.B) {
	w := NewWorld()
	for i := 0; i < 10000; i++ {
		e := w.CreateEntity()
		Add(w, e, pos{X: i})
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Query[pos](w, func(e Entity, p *pos) { _ = e; _ = p })
	}
}
