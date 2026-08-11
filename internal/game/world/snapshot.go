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
	snap.Weather = weatherOf(sim)
	return snap
}

// DeltaSnapshot 从 dirty（变更）+ removed（销毁）组装增量快照。
func DeltaSnapshot(sim *ecs.World, dirty []ecs.DirtyEntry, removed []ecs.Entity) *game.SnapshotDelta {
	delta := &game.SnapshotDelta{}
	states := make(map[ecs.Entity]*game.EntityState)
	removedComps := make(map[ecs.Entity][]string)
	for _, d := range dirty {
		for _, comp := range d.Comps {
			meta, ok := sim.Registry().MetaByName(comp)
			if !ok || meta.EncodeEntity == nil {
				continue
			}
			data, ok := meta.EncodeEntity(sim, d.Entity)
			if !ok {
				// dirty 但编码失败 = 组件本 tick 被移除（如 Workable 掉落转化）
				removedComps[d.Entity] = append(removedComps[d.Entity], string(meta.Name))
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
	for _, e := range sortedEntityKeys(removedComps) {
		delta.RemovedComponents = append(delta.RemovedComponents, &game.RemovedComponent{
			EntityId:   uint64(e),
			Components: removedComps[e],
		})
	}
	delta.DayCycle = dayCycleOf(sim)
	delta.Weather = weatherOf(sim)
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
	out := make([]*game.EntityState, 0, len(m))
	for _, id := range sortedEntityKeys(m) {
		out = append(out, m[id])
	}
	return out
}

// sortedEntityKeys 返回 map 的实体 ID 升序列表（确定性）。
func sortedEntityKeys[M ~map[ecs.Entity]V, V any](m M) []ecs.Entity {
	ids := make([]int, 0, len(m))
	for id := range m {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	out := make([]ecs.Entity, 0, len(ids))
	for _, id := range ids {
		out = append(out, ecs.Entity(id))
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

func weatherOf(sim *ecs.World) *game.WeatherState {
	wr, ok := ecs.TryResource[components.Weather](sim)
	if !ok {
		return nil
	}
	return &game.WeatherState{Phase: wr.Phase, Season: wr.Season()}
}
