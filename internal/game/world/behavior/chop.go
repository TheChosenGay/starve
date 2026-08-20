package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// ChopBehavior 砍伐：校验 Chopper ↔ Choppable，调用双方组件方法完成行为。
type ChopBehavior struct{}

func (ChopBehavior) CanDo(w *ecs.World, actor, target ecs.Entity) bool {
	if !w.IsAlive(actor) || ecs.Has[components.Dead](w, actor) || ecs.Has[components.Offline](w, actor) {
		return false
	}
	_, c := interactive.ActorCap[interactive.Chopper](w, actor)
	if c == nil || !ecs.Has[interactive.Choppable](w, target) {
		return false
	}
	if !withinRange(w, actor, target, c.Range) {
		return false
	}
	return ecs.Get[interactive.Choppable](w, target).Usable(w, target)
}

func (b ChopBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	if !b.CanDo(w, actor, target) {
		return false
	}
	src, c := interactive.ActorCap[interactive.Chopper](w, actor)
	t := ecs.Get[interactive.Choppable](w, target)
	if t.BeChopped(w, target, c.Efficiency) {
		applyDepletion(w, target, true)
	}
	c.Use(w, src)
	return true
}
