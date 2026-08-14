package ecs

import "testing"

// 掩码在 Add/Remove 上增加的开销：一个 maskSet（跨界检查 + 位操作）。
func BenchmarkWorldAddRemove(b *testing.B) {
	w := NewWorld()
	e := w.CreateEntity()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Add(w, e, pos{X: i})
		Remove[pos](w, e)
	}
}

// 占位组件：给世界"注册"多个存储类型，模拟组件类型多的场景。
type d0 struct{}
type d1 struct{}
type d2 struct{}
type d3 struct{}
type d4 struct{}
type d5 struct{}
type d6 struct{}
type d7 struct{}
type d8 struct{}
type d9 struct{}
type d10 struct{}
type d11 struct{}

func registerDummyStorages(w *World) Entity {
	holder := w.CreateEntity()
	Add(w, holder, d0{})
	Add(w, holder, d1{})
	Add(w, holder, d2{})
	Add(w, holder, d3{})
	Add(w, holder, d4{})
	Add(w, holder, d5{})
	Add(w, holder, d6{})
	Add(w, holder, d7{})
	Add(w, holder, d8{})
	Add(w, holder, d9{})
	Add(w, holder, d10{})
	Add(w, holder, d11{})
	return holder
}

// 销毁基准：世界注册了 13 个存储类型（12 占位 + pos），但实体只挂 1 个组件。
// 掩码方案下销毁只遍历实体的实际组件（O(1)），不随存储类型数增长。
func BenchmarkWorldDestroySparse(b *testing.B) {
	w := NewWorld()
	registerDummyStorages(w)
	const pool = 1000
	ents := make([]Entity, pool)
	for i := range ents {
		e := w.CreateEntity()
		Add(w, e, pos{X: i})
		ents[i] = e
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := ents[i%pool]
		w.DestroyEntity(e)
		e2 := w.CreateEntity() // 复用 ID，维持池大小
		Add(w, e2, pos{X: i})
		ents[i%pool] = e2
	}
}

// 对照：世界只注册了 pos 一个存储类型时的销毁开销。
// 与 DestroySparse（13 个存储类型）对比可验证销毁成本不随存储类型数增长。
func BenchmarkWorldDestroyMinimal(b *testing.B) {
	w := NewWorld()
	const pool = 1000
	ents := make([]Entity, pool)
	for i := range ents {
		e := w.CreateEntity()
		Add(w, e, pos{X: i})
		ents[i] = e
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := ents[i%pool]
		w.DestroyEntity(e)
		e2 := w.CreateEntity()
		Add(w, e2, pos{X: i})
		ents[i%pool] = e2
	}
}

// 销毁基准：实体挂 4 个组件（掩码 4 位），对比单组件场景的销毁开销。
func BenchmarkWorldDestroyDense(b *testing.B) {
	w := NewWorld()
	registerDummyStorages(w)
	const pool = 1000
	ents := make([]Entity, pool)
	for i := range ents {
		e := w.CreateEntity()
		Add(w, e, pos{X: i})
		Add(w, e, d0{})
		Add(w, e, d1{})
		Add(w, e, d2{})
		ents[i] = e
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e := ents[i%pool]
		w.DestroyEntity(e)
		e2 := w.CreateEntity()
		Add(w, e2, pos{X: i})
		Add(w, e2, d0{})
		Add(w, e2, d1{})
		Add(w, e2, d2{})
		ents[i%pool] = e2
	}
}
