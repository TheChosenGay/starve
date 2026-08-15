package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

func init() {
	// 攻击：Attacker（-er）↔ 目标 Health（活物）。伤害经 Health.TakeDamage 减免。
	// 与工作量型（砍/挖/采）不同，单独注册（需要 components.Health，故放世界层）。
	interactive.RegisterPair(interactive.Pair{
		Intent: interactive.IntentAttack, Active: "Attacker", Reactive: "Health",
		Range: func(w *ecs.World, actor ecs.Entity) (int, bool) {
			if !ecs.Has[interactive.Attacker](w, actor) {
				return 0, false
			}
			return ecs.Get[interactive.Attacker](w, actor).AttackRange, true
		},
		Match: func(w *ecs.World, actor, target ecs.Entity) bool {
			return ecs.Has[interactive.Attacker](w, actor) &&
				ecs.Has[components.Health](w, target) &&
				w.IsAlive(target) && !ecs.Has[components.Dead](w, target)
		},
		Apply: func(w *ecs.World, actor, target ecs.Entity) (interactive.DoResult, bool) {
			a := ecs.Get[interactive.Attacker](w, actor)
			// 伤害减免 + 受击副作用（AI 标记/仇恨/打断）由 Health 所在包闭环。
			dealt := components.ApplyDamage(w, target, actor, a.AttackDamage)
			return interactive.DoResult{DamageDealt: dealt}, true
		},
	})
}
