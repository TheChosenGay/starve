package effect

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// coldEffect 寒冷（冻伤）：持续覆盖时每秒脉冲扣血，param = 每次脉冲冻伤量。
// 由天气系统派生（温度 ≤ 阈值），不是发射器/地块来源。
type coldEffect struct{}

func (coldEffect) Order() EffectOrder { return components.EffectCold }

func (coldEffect) Interval() time.Duration { return time.Second }

func (coldEffect) OnEnter(*ecs.World, ecs.Entity, int) {}

func (coldEffect) OnTick(w *ecs.World, e ecs.Entity, param int) {
	drainHealth(w, e, param, game.HealthChangeCause_HEALTH_CHANGE_CAUSE_WEATHER)
}

func (coldEffect) OnExit(*ecs.World, ecs.Entity, int) {}

func init() { RegisterEffect(coldEffect{}) }
