package effect

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// coldEffect 寒冷（冻伤）：持续覆盖时每 tick 扣血，param = 每 tick 冻伤量。
// 由天气系统派生（温度 ≤ 阈值），不是发射器/地块来源。
type coldEffect struct{}

func (coldEffect) Order() EffectOrder { return components.EffectCold }

func (coldEffect) OnEnter(*ecs.World, ecs.Entity, int) {}

func (coldEffect) OnTick(w *ecs.World, e ecs.Entity, param int) {
	drainHealth(w, e, param)
}

func (coldEffect) OnExit(*ecs.World, ecs.Entity, int) {}

func init() { RegisterEffect(coldEffect{}) }
