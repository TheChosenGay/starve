package effect

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// heatEffect 炎热（中暑）：持续覆盖时每 tick 扣血，param = 每 tick 中暑量。
// 由天气系统派生（温度 ≥ 阈值）。
type heatEffect struct{}

func (heatEffect) Order() EffectOrder { return components.EffectHeat }

func (heatEffect) OnEnter(*ecs.World, ecs.Entity, int) {}

func (heatEffect) OnTick(w *ecs.World, e ecs.Entity, param int) {
	drainHealth(w, e, param, game.HealthChangeCause_HEALTH_CHANGE_CAUSE_WEATHER)
}

func (heatEffect) OnExit(*ecs.World, ecs.Entity, int) {}

func init() { RegisterEffect(heatEffect{}) }
