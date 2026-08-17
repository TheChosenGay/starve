package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components/interactive"
)

// MineBehavior 挖掘：校验 Miner ↔ Minable。
type MineBehavior struct{}

func (MineBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	src, c := interactive.ActorCap[interactive.Miner](w, actor)
	if c == nil || !ecs.Has[interactive.Minable](w, target) {
		return false
	}
	if !withinRange(w, actor, target, c.Range) {
		return false
	}
	t := ecs.Get[interactive.Minable](w, target)
	if t.WorkLeft <= 0 {
		return false
	}
	if t.BeMined(w, target, c.Efficiency) {
		applyDepletion(w, target, true)
	}
	c.Use(w, src)
	return true
}
