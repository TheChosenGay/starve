package effect

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// poisonEffect 中毒：持续覆盖时每 tick 扣血（毒水/毒沼地块、毒花等）。
// param = 每 tick 扣血量（0 = 默认 1）；10Hz 下 param=1 ≈ 每秒 10 点。
type poisonEffect struct{}

func (poisonEffect) Order() EffectOrder { return components.EffectPoison }

func (poisonEffect) OnEnter(*ecs.World, ecs.Entity, int) {}

func (poisonEffect) OnTick(w *ecs.World, e ecs.Entity, param int) {
	if !ecs.Has[components.Health](w, e) {
		return
	}
	h := ecs.Get[components.Health](w, e)
	if h.Cur <= 0 {
		return
	}
	if param <= 0 {
		param = 1
	}
	h.Cur -= param
	if h.Cur < 0 {
		h.Cur = 0
	}
	ecs.MarkDirty[components.Health](w, e)
}

func (poisonEffect) OnExit(*ecs.World, ecs.Entity, int) {}

func init() { RegisterEffect(poisonEffect{}) }
