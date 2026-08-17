package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// AttackBehavior 攻击：校验 Attacker ↔ Attackable；伤害由 Attackable 依赖 Defense/Health 结算。
type AttackBehavior struct{}

func (AttackBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	_, a := interactive.ActorCap[interactive.Attacker](w, actor)
	if a == nil || !ecs.Has[components.Attackable](w, target) {
		return false
	}
	if !withinRange(w, actor, target, a.AttackRange) {
		return false
	}
	ecs.Get[components.Attackable](w, target).ApplyDamage(w, target, actor, a.AttackDamage)
	return true
}
