package effect

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// poisonEffect 中毒：持续覆盖时每 tick 扣血（毒水/毒沼地块、毒花等）。
// param = 每 tick 扣血量（0 = 默认 1）；20Hz 下 param=1 ≈ 每秒 20 点。
type poisonEffect struct{}

func (poisonEffect) Order() EffectOrder { return components.EffectPoison }

func (poisonEffect) OnEnter(*ecs.World, ecs.Entity, int) {}

func (poisonEffect) OnTick(w *ecs.World, e ecs.Entity, param int) {
	drainHealth(w, e, param)
}

func (poisonEffect) OnExit(*ecs.World, ecs.Entity, int) {}

func init() { RegisterEffect(poisonEffect{}) }
