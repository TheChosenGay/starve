package behavior

import (
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// Register 注册全部行为（世界包在 init 时调用）。
func Register() {
	interactive.Register(interactive.IntentChop, ChopBehavior{})
	interactive.Register(interactive.IntentMine, MineBehavior{})
	interactive.Register(interactive.IntentPick, PickBehavior{})
	interactive.Register(interactive.IntentAttack, AttackBehavior{})
	interactive.Register(interactive.IntentPickup, PickupBehavior{})

	// 自动行为候选：activer ↔ actived ↔ behavior（一行声明，映射关系即注册本身）
	RegisterPair[interactive.Chopper, interactive.Choppable](interactive.IntentChop, ChopBehavior{})
	RegisterPair[interactive.Miner, interactive.Minable](interactive.IntentMine, MineBehavior{})
	RegisterPair[interactive.Picker, interactive.Pickable](interactive.IntentPick, PickBehavior{})
	RegisterPair[interactive.Attacker, components.Attackable](interactive.IntentAttack, AttackBehavior{})
	RegisterPair[interactive.Looter, components.Lootable](interactive.IntentPickup, PickupBehavior{})
}
