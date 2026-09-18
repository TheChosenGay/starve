package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// FuelSystem 燃料消耗（order 15）：Fuel.Cur 每 tick 递减，烧完即熄灭。
//
// 唯一的不变量：**Fuel.Cur > 0 ⇔ 实体有 HeatSource**。命令层添柴只改 Cur，
// 点燃/熄灭只在本系统一处落笔，避免"命令加了热源、系统这一 tick 又判它该熄"
// 的两处真相（火焰表现与实际供暖必须同源）。
//
// 为什么排在天气（order 20）/效果（order 90）之前：温度采样（weather 包）
// 与 EffectSystem 都在本 tick 稍后读 HeatSource。燃料先结算，同一 tick 内
// "柴烧完了"和"不再供暖"才是一致的；排到后面会让熄灭晚一 tick 才生效，
// 玩家看到火焰没了却还在回温。
type FuelSystem struct {
	BurnPerTick int // 每 tick 消耗的燃料；<= 0 用 1
}

// Update 实现 ECS 系统接口。
func (s *FuelSystem) Update(w *ecs.World, dt time.Duration) {
	burn := s.BurnPerTick
	if burn <= 0 {
		burn = 1
	}
	ecs.Query[components.Fuel](w, func(e ecs.Entity, f *components.Fuel) {
		if f.Cur > 0 {
			f.Cur -= burn
			if f.Cur < 0 {
				f.Cur = 0
			}
			// 燃料是持续变化的量：**动了就标脏**，不能只在"刚好归零"这种拐点标脏
			// （docs/P1.3 §11 同一契约）。否则快照里的剩余燃料会长期停在旧值，
			// 等真归零时客户端才看到它跳变。
			ecs.MarkDirty[components.Fuel](w, e)
		}
		if f.Cur > 0 {
			// 幂等：着着的火堆每 tick 走这里也不产生任何组件变更/快照流量。
			components.RelightHeatSource(w, e, f)
			return
		}
		// 烧完（或从来只有空燃料）= 熄灭。Remove 幂等，所以熄灭后的火堆
		// 每 tick 走到这里都是空操作——静止实体不该继续刷快照。
		components.ExtinguishHeatSource(w, e)
	})
}
