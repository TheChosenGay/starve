package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// MineBehavior 挖掘：校验 Miner ↔ Minable。
type MineBehavior struct{}

func (MineBehavior) CanDo(w *ecs.World, actor, target ecs.Entity) bool {
	if !w.IsAlive(actor) || ecs.Has[components.Dead](w, actor) || ecs.Has[components.Offline](w, actor) {
		return false
	}
	_, c := interactive.ActorCap[interactive.Miner](w, actor)
	if c == nil || !ecs.Has[interactive.Minable](w, target) {
		return false
	}
	if !withinRange(w, actor, target, c.Range) {
		return false
	}
	return ecs.Get[interactive.Minable](w, target).Usable(w, target)
}

func (b MineBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	if !b.CanDo(w, actor, target) {
		return false
	}
	src, c := interactive.ActorCap[interactive.Miner](w, actor)
	t := ecs.Get[interactive.Minable](w, target)
	if t.BeMined(w, target, c.Efficiency) {
		applyDepletion(w, target, true)
	}
	c.Use(w, src)
	return true
}
