package ecs

import "testing"

func TestDirtyOnComponentOps(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	Add(w, e, hp{})
	Set(w, e, hp{Cur: 5})
	Add(w, e, pos{X: 1})
	Remove[hp](w, e)

	entries := w.DrainDirtySorted()
	if len(entries) != 1 {
		t.Fatalf("entries = %+v", entries)
	}
	got := entries[0]
	if got.Entity != e {
		t.Fatalf("entity = %d", got.Entity)
	}
	// 实体上的组件按名称排序去重："hp" < "pos"
	if len(got.Comps) != 2 || got.Comps[0] != "hp" || got.Comps[1] != "pos" {
		t.Fatalf("comps = %v", got.Comps)
	}
	if len(w.DrainDirtySorted()) != 0 {
		t.Fatal("dirty not cleared")
	}
}

func TestDrainDirtyMap(t *testing.T) {
	w := NewWorld()
	a := w.CreateEntity()
	b := w.CreateEntity()
	Add(w, a, hp{})
	Add(w, b, pos{})
	m := w.DrainDirty()
	if len(m) != 2 || len(m[a]) != 1 || m[a][0] != "hp" || m[b][0] != "pos" {
		t.Fatalf("map = %+v", m)
	}
}

func TestMarkDirtyManual(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	Add(w, e, hp{})
	w.MarkDirty(e, hp{}) // Get 指针修改后手动标记
	entries := w.DrainDirtySorted()
	if len(entries) != 1 || len(entries[0].Comps) != 1 || entries[0].Comps[0] != "hp" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestDestroyEntityMarksDirty(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	Add(w, e, hp{})
	Add(w, e, pos{})
	w.DrainDirtySorted() // 清掉 Add 的标记
	w.DestroyEntity(e)
	entries := w.DrainDirtySorted()
	if len(entries) != 1 || len(entries[0].Comps) != 2 {
		t.Fatalf("entries = %+v", entries)
	}
}
