package effect

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// speedEffect 速度修正：加速/减速是同一个效果，方向完全看 param
// （正数加速、负数减速，如 +100 = 移动效率翻倍，-50 = 减半）。
type speedEffect struct{}

func (speedEffect) Order() EffectOrder { return components.EffectSpeed }

func (speedEffect) SpeedModPercent(param int) int { return param }

func (speedEffect) OnEnter(*ecs.World, ecs.Entity, int) {}
func (speedEffect) OnTick(*ecs.World, ecs.Entity, int)  {}
func (speedEffect) OnExit(*ecs.World, ecs.Entity, int)  {}

func init() { RegisterEffect(speedEffect{}) }
