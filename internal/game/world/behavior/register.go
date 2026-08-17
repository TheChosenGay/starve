package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// Register 注册全部行为（世界包在 init 时调用）。
func Register() {
	interactive.Register(interactive.IntentChop, ChopBehavior{})
	interactive.Register(interactive.IntentMine, MineBehavior{})
	interactive.Register(interactive.IntentPick, PickBehavior{})
	interactive.Register(interactive.IntentAttack, AttackBehavior{})

	// 自动行为候选：-er ↔ -able ↔ Behavior 匹配
	RegisterPair(CapabilityPair{
		Intent: interactive.IntentChop, Active: "Chopper", Reactive: "Choppable",
		HasActive: func(w *ecs.World, actor ecs.Entity) bool {
			_, c := interactive.ActorCap[interactive.Chopper](w, actor)
			return c != nil
		},
		Range: func(w *ecs.World, actor ecs.Entity) int {
			if _, c := interactive.ActorCap[interactive.Chopper](w, actor); c != nil {
				return c.Range
			}
			return 0
		},
		Nearest: func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity {
			return nearestWith[interactive.Choppable](w, actor, radius, func(e ecs.Entity) bool {
				return ecs.Get[interactive.Choppable](w, e).WorkLeft > 0
			})
		},
	})
	RegisterPair(CapabilityPair{
		Intent: interactive.IntentMine, Active: "Miner", Reactive: "Minable",
		HasActive: func(w *ecs.World, actor ecs.Entity) bool {
			_, c := interactive.ActorCap[interactive.Miner](w, actor)
			return c != nil
		},
		Range: func(w *ecs.World, actor ecs.Entity) int {
			if _, c := interactive.ActorCap[interactive.Miner](w, actor); c != nil {
				return c.Range
			}
			return 0
		},
		Nearest: func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity {
			return nearestWith[interactive.Minable](w, actor, radius, func(e ecs.Entity) bool {
				return ecs.Get[interactive.Minable](w, e).WorkLeft > 0
			})
		},
	})
	RegisterPair(CapabilityPair{
		Intent: interactive.IntentPick, Active: "Picker", Reactive: "Pickable",
		HasActive: func(w *ecs.World, actor ecs.Entity) bool {
			_, c := interactive.ActorCap[interactive.Picker](w, actor)
			return c != nil
		},
		Range: func(w *ecs.World, actor ecs.Entity) int {
			if _, c := interactive.ActorCap[interactive.Picker](w, actor); c != nil {
				return c.Range
			}
			return 0
		},
		Nearest: func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity {
			return nearestWith[interactive.Pickable](w, actor, radius, func(e ecs.Entity) bool {
				return ecs.Get[interactive.Pickable](w, e).WorkLeft > 0
			})
		},
	})
	RegisterPair(CapabilityPair{
		Intent: interactive.IntentAttack, Active: "Attacker", Reactive: "Attackable",
		HasActive: func(w *ecs.World, actor ecs.Entity) bool {
			_, c := interactive.ActorCap[interactive.Attacker](w, actor)
			return c != nil
		},
		Range: func(w *ecs.World, actor ecs.Entity) int {
			if _, c := interactive.ActorCap[interactive.Attacker](w, actor); c != nil {
				return c.AttackRange
			}
			return 0
		},
		Nearest: func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity {
			return nearestWith[components.Attackable](w, actor, radius, func(e ecs.Entity) bool {
				return w.IsAlive(e) && ecs.Has[components.Health](w, e) &&
					ecs.Get[components.Health](w, e).Cur > 0 && !ecs.Has[components.Dead](w, e)
			})
		},
	})
}
