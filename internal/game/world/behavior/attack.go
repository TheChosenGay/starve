package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// AttackBehavior 攻击：校验 Attacker ↔ Attackable；伤害由 Attackable 依赖 Defense/Health 结算。
type AttackBehavior struct{}

func (AttackBehavior) CanDo(w *ecs.World, actor, target ecs.Entity) bool {
	if !w.IsAlive(actor) || ecs.Has[components.Dead](w, actor) || ecs.Has[components.Offline](w, actor) {
		return false
	}
	_, a := interactive.ActorCap[interactive.Attacker](w, actor)
	if a == nil || !ecs.Has[components.Attackable](w, target) {
		return false
	}
	if !ecs.Get[components.Attackable](w, target).Usable(w, target) {
		return false
	}
	return withinRange(w, actor, target, a.AttackRange)
}

func (b AttackBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	return b.DoResult(w, actor, target).Success
}

func (b AttackBehavior) DoResult(w *ecs.World, actor, target ecs.Entity) interactive.InteractionResult {
	if !b.CanDo(w, actor, target) {
		return interactive.InteractionResult{}
	}
	_, a := interactive.ActorCap[interactive.Attacker](w, actor)
	damage := ecs.Get[components.Attackable](w, target).ApplyDamage(w, target, actor, a.AttackDamage)
	return interactive.InteractionResult{Success: damage.Attempted, Damage: &damage}
}
