package behavior

import "starve/internal/game/components/interactive"

// Register 注册全部行为（世界包在 init 时调用）。
func Register() {
	interactive.Register(interactive.IntentChop, ChopBehavior{})
	interactive.Register(interactive.IntentMine, MineBehavior{})
	interactive.Register(interactive.IntentPick, PickBehavior{})
	interactive.Register(interactive.IntentAttack, AttackBehavior{})
}
