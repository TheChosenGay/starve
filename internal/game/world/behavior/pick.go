package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// PickBehavior 采集：校验 Picker ↔ Pickable（裸手默认 Picker）。
type PickBehavior struct{}

func (PickBehavior) CanDo(w *ecs.World, actor, target ecs.Entity) bool {
	if !w.IsAlive(actor) || ecs.Has[components.Dead](w, actor) || ecs.Has[components.Offline](w, actor) {
		return false
	}
	_, c := interactive.ActorCap[interactive.Picker](w, actor)
	if c == nil || !ecs.Has[interactive.Pickable](w, target) {
		return false
	}
	if !withinRange(w, actor, target, c.Range) {
		return false
	}
	return ecs.Get[interactive.Pickable](w, target).Usable(w, target)
}

func (b PickBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	if !b.CanDo(w, actor, target) {
		return false
	}
	src, c := interactive.ActorCap[interactive.Picker](w, actor)
	t := ecs.Get[interactive.Pickable](w, target)
	if t.BePicked(w, target, c.Efficiency) {
		applyDepletion(w, target, false)
	}
	c.Use(w, src)
	return true
}
