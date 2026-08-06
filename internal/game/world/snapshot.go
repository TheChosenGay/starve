package world

import (
	"sort"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// FullSnapshot 组装全量快照：遍历注册表所有已注册 codec 的组件类型。
// 组件顺序 = 类型名排序，实体顺序 = ID 升序（确定性）。
func FullSnapshot(sim *ecs.World) *game.Snapshot {
	snap := &game.Snapshot{}
	states := make(map[ecs.Entity]*game.EntityState)
	for _, t := range sim.Registry().Types() {
		meta, ok := sim.Registry().Meta(t)
		if !ok || meta.Snapshot == nil {
			continue
		}
		for _, cs := range meta.Snapshot(sim) {
			st := entityState(states, cs.Entity)
			st.Components = append(st.Components, &game.ComponentState{
				Component: string(meta.Name),
				Data:      cs.Data,
			})
		}
	}
	snap.Entities = sortedStates(states)
	snap.DayCycle = dayCycleOf(sim)
	return snap
}

// DeltaSnapshot 从 dirty（变更）+ removed（销毁）组装增量快照。
func DeltaSnapshot(sim *ecs.World, dirty []ecs.DirtyEntry, removed []ecs.Entity) *game.SnapshotDelta {
	delta := &game.SnapshotDelta{}
	states := make(map[ecs.Entity]*game.EntityState)
	for _, d := range dirty {
		for _, comp := range d.Comps {
			meta, ok := sim.Registry().MetaByName(comp)
			if !ok || meta.EncodeEntity == nil {
				continue
			}
			data, ok := meta.EncodeEntity(sim, d.Entity)
			if !ok {
				continue
			}
			st := entityState(states, d.Entity)
			st.Components = append(st.Components, &game.ComponentState{
				Component: string(meta.Name),
				Data:      data,
			})
		}
	}
	delta.Entities = sortedStates(states)
	for _, e := range removed {
		delta.RemovedEntities = append(delta.RemovedEntities, uint64(e))
	}
	delta.DayCycle = dayCycleOf(sim)
	return delta
}

func entityState(m map[ecs.Entity]*game.EntityState, e ecs.Entity) *game.EntityState {
	st := m[e]
	if st == nil {
		st = &game.EntityState{EntityId: uint64(e)}
		m[e] = st
	}
	return st
}

func sortedStates(m map[ecs.Entity]*game.EntityState) []*game.EntityState {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	out := make([]*game.EntityState, 0, len(ids))
	for _, id := range ids {
		out = append(out, m[ecs.Entity(id)])
	}
	return out
}

func dayCycleOf(sim *ecs.World) *game.DayCycle {
	dc, ok := ecs.TryResource[components.DayCycle](sim)
	if !ok {
		return nil
	}
	return &game.DayCycle{Phase: int32(dc.Phase), Light: dc.Light}
}
