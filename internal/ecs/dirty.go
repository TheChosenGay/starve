package ecs

import (
	"reflect"
	"sort"
)

// DirtyEntry 是一次脏标记快照：实体 + 变更过的组件。
type DirtyEntry struct {
	Entity Entity
	Comps  []ComponentID
}

type dirtyOp struct {
	e    Entity
	comp ComponentID
}

func (w *World) markDirty(e Entity, comp ComponentID) {
	w.dirty = append(w.dirty, dirtyOp{e: e, comp: comp})
}

// MarkDirty 手动标记组件变更（用于通过 Get 指针直接修改的场景）。
// comps 传组件实例即可，如 MarkDirty(e, components.Health{})。
func (w *World) MarkDirty(e Entity, comps ...any) {
	for _, c := range comps {
		t := reflect.TypeOf(c)
		if t == nil {
			continue
		}
		w.registry.ensure(t)
		w.markDirty(e, ComponentID(w.registry.Name(t)))
	}
}

// DrainDirty 取走并清空脏标记，按设计文档契约返回 map[Entity][]ComponentID。
// 注意：map 的遍历顺序随机；需要确定性消费时用 DrainDirtySorted。
func (w *World) DrainDirty() map[Entity][]ComponentID {
	entries := w.DrainDirtySorted()
	m := make(map[Entity][]ComponentID, len(entries))
	for _, e := range entries {
		m[e.Entity] = e.Comps
	}
	return m
}

// DrainDirtySorted 取走并清空脏标记，按实体 ID 升序返回（确定性）。
// 同一实体上的组件按组件名排序并去重。
func (w *World) DrainDirtySorted() []DirtyEntry {
	if len(w.dirty) == 0 {
		w.dirty = nil
		return nil
	}
	ops := w.dirty
	w.dirty = nil
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].e != ops[j].e {
			return ops[i].e < ops[j].e
		}
		return ops[i].comp < ops[j].comp
	})
	var out []DirtyEntry
	for i := 0; i < len(ops); {
		e := ops[i].e
		comps := make([]ComponentID, 0, 4)
		last := ComponentID("")
		for ; i < len(ops) && ops[i].e == e; i++ {
			if ops[i].comp != last {
				comps = append(comps, ops[i].comp)
				last = ops[i].comp
			}
		}
		out = append(out, DirtyEntry{Entity: e, Comps: comps})
	}
	return out
}
