package effect

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// poisonEffect 中毒：持续覆盖时每秒脉冲扣血（毒水/毒沼地块、毒花等）。
// param = 每次脉冲扣血量（0 = 默认 1），不随服务端 tick 频率变化。
type poisonEffect struct{}

func (poisonEffect) Order() EffectOrder { return components.EffectPoison }

func (poisonEffect) Interval() time.Duration { return time.Second }

func (poisonEffect) OnEnter(*ecs.World, ecs.Entity, int) {}

func (poisonEffect) OnTick(w *ecs.World, e ecs.Entity, param int) {
	drainHealth(w, e, param, game.HealthChangeCause_HEALTH_CHANGE_CAUSE_POISON)
}

func (poisonEffect) OnExit(*ecs.World, ecs.Entity, int) {}

func init() { RegisterEffect(poisonEffect{}) }
