package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components/interactive"
)

// ChopBehavior 砍伐：校验 Chopper ↔ Choppable，调用双方组件方法完成行为。
type ChopBehavior struct{}

func (ChopBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	src, c := interactive.ActorCap[interactive.Chopper](w, actor)
	if c == nil || !ecs.Has[interactive.Choppable](w, target) {
		return false
	}
	if !withinRange(w, actor, target, c.Range) {
		return false
	}
	t := ecs.Get[interactive.Choppable](w, target)
	if t.WorkLeft <= 0 {
		return false
	}
	if t.BeChopped(w, target, c.Efficiency) {
		applyDepletion(w, target, true)
	}
	c.Use(w, src)
	return true
}
