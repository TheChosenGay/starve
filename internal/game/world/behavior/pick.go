package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components/interactive"
)

// PickBehavior 采集：校验 Picker ↔ Pickable（裸手默认 Picker）。
type PickBehavior struct{}

func (PickBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	_, c := interactive.ActorCap[interactive.Picker](w, actor)
	if c == nil || !ecs.Has[interactive.Pickable](w, target) {
		return false
	}
	if !withinRange(w, actor, target, c.Range) {
		return false
	}
	t := ecs.Get[interactive.Pickable](w, target)
	if t.WorkLeft <= 0 {
		return false
	}
	if t.BePicked(w, target, c.Efficiency) {
		applyDepletion(w, target, false)
	}
	c.Use(w, actor)
	return true
}
