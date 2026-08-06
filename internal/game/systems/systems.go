// Package systems 存放玩法系统（M5）。
// 约束（规划文档 §7）：纯函数——不调 actor API、不发网络消息；
// 指针直改组件后手动 MarkDirty，变更才会进增量快照。
package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// Config 是玩法系统的参数（世界级默认值；实体级差异放组件字段）。
type Config struct {
	HungerDefaultRate int // 实体未指定 Hunger.Rate 时用此速率
	GrowthTicks       int // 可生长实体每多少 tick 长一阶段
}

// 系统固定顺序（规划文档 §7：order 冲突报错，阶段间留间隔）。
const (
	OrderDayNight   = 10
	OrderHunger     = 100
	OrderStarvation = 105
	OrderGrowth     = 110
	OrderDeath      = 130
)

// RegisterAll 注册全部玩法系统（固定顺序）。
// 系统数量增长时按域拆分到子文件（如 hunger.go / death.go），在此统一装配。
func RegisterAll(w *ecs.World, cfg Config) {
	if cfg.HungerDefaultRate <= 0 {
		cfg.HungerDefaultRate = 1
	}
	if cfg.GrowthTicks <= 0 {
		cfg.GrowthTicks = 20
	}
	w.AddSystem(OrderDayNight, &DayNightSystem{})
	w.AddSystem(OrderHunger, &HungerSystem{DefaultRate: cfg.HungerDefaultRate})
	w.AddSystem(OrderStarvation, &StarvationSystem{HealthDrain: 1})
	w.AddSystem(OrderGrowth, &GrowthSystem{TicksPerStage: cfg.GrowthTicks})
	w.AddSystem(OrderDeath, &DeathSystem{})
}

// DayNightSystem 昼夜推进（order 10）：推进 Resource.DayCycle。
type DayNightSystem struct{}

func (s *DayNightSystem) Update(w *ecs.World, dt time.Duration) {
	dc := ecs.Resource[components.DayCycle](w)
	dc.Phase++
	// 简化光照：24 阶段循环，确定性整数运算
	dc.Light = float32(dc.Phase%24) / 24
}

// HungerSystem 饥饿消耗（order 100）：有 Hunger 的实体每 tick 按速率扣减。
// 速率优先取实体组件的 Hunger.Rate（不同角色可不同），0 时用世界默认。
type HungerSystem struct {
	DefaultRate int
}

func (s *HungerSystem) Update(w *ecs.World, dt time.Duration) {
	ecs.Query[components.Hunger](w, func(e ecs.Entity, h *components.Hunger) {
		if h.Level <= 0 {
			return
		}
		rate := h.Rate
		if rate <= 0 {
			rate = s.DefaultRate
		}
		h.Level -= rate
		if h.Level < 0 {
			h.Level = 0
		}
		ecs.MarkDirty[components.Hunger](w, e)
	})
}

// StarvationSystem 饥饿掉血（order 105）：Hunger<=0 的实体每 tick 扣血。
// 设计：饿死不是瞬间死亡，而是持续掉血直到 Health<=0。
type StarvationSystem struct {
	HealthDrain int
}

func (s *StarvationSystem) Update(w *ecs.World, dt time.Duration) {
	drain := s.HealthDrain
	if drain <= 0 {
		drain = 1
	}
	ecs.Query2[components.Hunger, components.Health](w, func(e ecs.Entity, h *components.Hunger, hp *components.Health) {
		if h.Level > 0 || hp.Cur <= 0 {
			return
		}
		hp.Cur -= drain
		if hp.Cur < 0 {
			hp.Cur = 0
		}
		ecs.MarkDirty[components.Health](w, e)
	})
}

// GrowthSystem 生长（order 110）：Growable 每 N tick 长一阶段。
type GrowthSystem struct {
	TicksPerStage int
}

func (s *GrowthSystem) Update(w *ecs.World, dt time.Duration) {
	n := s.TicksPerStage
	if n <= 0 {
		n = 20
	}
	ecs.Query[components.Growable](w, func(e ecs.Entity, g *components.Growable) {
		g.Ticks++
		if g.Ticks >= n {
			g.Stage++
			g.Ticks = 0
			ecs.MarkDirty[components.Growable](w, e)
		}
	})
}

// DeathSystem 死亡结算（order 130）：Health<=0 的实体打上 Dead 标记。
// 设计：不直接销毁实体——死后保留在世界上（尸体/幽灵状态），
// 由后续系统处理（掉落、重生、清理），客户端通过 Dead 组件呈现死亡。
type DeathSystem struct{}

func (s *DeathSystem) Update(w *ecs.World, dt time.Duration) {
	var dead []ecs.Entity
	ecs.Query[components.Health](w, func(e ecs.Entity, hp *components.Health) {
		if hp.Cur <= 0 && !ecs.Has[components.Dead](w, e) {
			dead = append(dead, e)
		}
	})
	for _, e := range dead {
		ecs.Add(w, e, components.Dead{Reason: "health_depleted"})
	}
}
