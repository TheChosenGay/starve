// Package effect 效果行为层（行为=注册表）：接口 + 注册表 + 具体效果实现。
// 数据组件（Effects/EffectEmitter/EffectOrder）在父包 components，
// 本包单向 import components（无环），系统层（systems）再 import 本包。
package effect

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// EffectOrder 复用父包的别名（proto 枚举，单一事实来源）。
type EffectOrder = components.EffectOrder

// Effect 效果行为接口：实例是共享单例（无状态），按 Order 注册；
// param 是覆盖源携带的强度参数（多源求和后传入），效果按 param 结算。
// 新效果 = 新枚举值 + 实现本接口 + init 里 RegisterEffect，零改动系统。
type Effect interface {
	Order() EffectOrder
	// OnEnter 进入覆盖（计数从 0 → >0）时调用一次。
	OnEnter(w *ecs.World, e ecs.Entity, param int)
	// OnTick 持续覆盖时每 tick 调用。
	OnTick(w *ecs.World, e ecs.Entity, param int)
	// OnExit 完全离开覆盖（计数归零）时调用一次。
	OnExit(w *ecs.World, e ecs.Entity, param int)
}

// PeriodicEffect 是低频结算效果的可选策略接口。
// EffectSystem 仍每 tick 计算覆盖关系，但只按 Interval 调用其 OnTick；
// 未实现本接口的效果继续逐 tick 执行，保持速度等连续效果的原语义。
type PeriodicEffect interface {
	Effect
	Interval() time.Duration
}

// registry 效果注册表（init 注册，EffectSystem 按 Order 查实现）。
var registry = map[EffectOrder]Effect{}

// RegisterEffect 登记一个效果实例（本包 init 调用）。
func RegisterEffect(ef Effect) {
	if ef == nil {
		return
	}
	if o := ef.Order(); o != 0 {
		registry[o] = ef
	}
}

// EffectFor 按 Order 取效果实现（未注册返回 nil）。
func EffectFor(order EffectOrder) Effect { return registry[order] }

// HasEffect 判断实体当前是否处于某效果覆盖中（Effects 组件计数 > 0）。
func HasEffect(w *ecs.World, e ecs.Entity, order EffectOrder) bool {
	if !ecs.Has[components.Effects](w, e) {
		return false
	}
	return ecs.Get[components.Effects](w, e).Has(order)
}

// SpeedModifier 速度修正效果（百分比）：param 即修正值，正数加速、负数减速。
// 加速/减速是同一个效果（EFFECT_ORDER_SPEED），方向完全看 param。
type SpeedModifier interface {
	SpeedModPercent(param int) int
}

// SpeedModPercent 返回实体当前全部速度修正之和（百分比；无效果 = 0）。
// 求和是交换运算，遍历 map 顺序不影响结果（确定性）。
func SpeedModPercent(w *ecs.World, e ecs.Entity) int {
	if !ecs.Has[components.Effects](w, e) {
		return 0
	}
	mod := 0
	for o, st := range ecs.Get[components.Effects](w, e).Active {
		if st.Count <= 0 {
			continue
		}
		if m, ok := registry[o].(SpeedModifier); ok {
			mod += m.SpeedModPercent(int(st.Param))
		}
	}
	return mod
}

// DerivedEffectsProvider 派生效果提供者：按世界状态为实体补充效果（order → param），
// 如天气温度 → 寒冷/炎热。与覆盖源（地块/发射器）同语义，EffectSystem 每 tick
// 并入覆盖集统一计算 Enter/Tick/Exit（不会被"覆盖集重算"误清除）。
type DerivedEffectsProvider func(w *ecs.World, e ecs.Entity) map[EffectOrder]int32

// derivedProviders 派生效果提供者清单（init 注册，EffectSystem 遍历汇总）。
var derivedProviders []DerivedEffectsProvider

// RegisterDerivedEffectsProvider 登记一个派生效果提供者（init 调用）。
func RegisterDerivedEffectsProvider(fn DerivedEffectsProvider) {
	if fn != nil {
		derivedProviders = append(derivedProviders, fn)
	}
}

// DerivedEffectsFor 汇总全部提供者的派生效果（order → 参数求和；空 map 无效果）。
func DerivedEffectsFor(w *ecs.World, e ecs.Entity) map[EffectOrder]int32 {
	out := map[EffectOrder]int32{}
	for _, fn := range derivedProviders {
		for o, prm := range fn(w, e) {
			if o == 0 {
				continue
			}
			out[o] += prm
		}
	}
	return out
}
