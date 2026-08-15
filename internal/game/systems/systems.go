// Package systems 存放玩法系统（M5）。
// 约束（规划文档 §7）：纯函数——不调 actor API、不发网络消息；
// 指针直改组件后手动 MarkDirty，变更才会进增量快照。
package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// Config 是玩法系统的参数（世界级默认值；实体级差异放组件字段）。
type Config struct {
	GrowthTicks int // 可生长实体每多少 tick 长一阶段
	AOIInterval int // AOI 感知刷新间隔（tick）；0 = 默认 4
}

// SystemOrder 系统固定顺序（规划文档 §7：order 冲突报错，阶段间留间隔）。
const (
	SystemOrderDayNight   = 10
	SystemOrderWeather    = 20 // 天气推进：先于效果/移动（采样用最新相位）
	SystemOrderEffect     = 90 // 效果结算：先于移动/生存，速度修正同 tick 生效
	SystemOrderAOI        = 91 // 感知结算：先于生物决策（Visible 供仇恨使用）
	SystemOrderAI         = 92 // 生物 AI 状态机：先于移动（决策写入移动队列）
	SystemOrderMove       = 95 // 移动推进：消费效果后的速度
	SystemOrderHunger     = 100
	SystemOrderStarvation = 105
	SystemOrderGrowth     = 110
	SystemOrderRespawn    = 115
	SystemOrderCraft      = 120
	SystemOrderDeath      = 130
)

// RegisterAll 注册全部玩法系统（固定顺序）。
// 系统数量增长时按域拆分到子文件（如 hunger.go / death.go），在此统一装配。
func RegisterAll(w *ecs.World, cfg Config) {
	if cfg.GrowthTicks <= 0 {
		cfg.GrowthTicks = 20
	}
	if cfg.AOIInterval <= 0 {
		cfg.AOIInterval = 4
	}
	w.AddSystem(SystemOrderDayNight, &DayNightSystem{})
	w.AddSystem(SystemOrderWeather, &WeatherSystem{})
	w.AddSystem(SystemOrderEffect, &EffectSystem{})
	w.AddSystem(SystemOrderAOI, &AOISystem{Interval: cfg.AOIInterval})
	w.AddSystem(SystemOrderAI, &AISystem{})
	w.AddSystem(SystemOrderMove, &MoveSystem{})
	w.AddSystem(SystemOrderHunger, &HungerSystem{})
	w.AddSystem(SystemOrderStarvation, &StarvationSystem{HealthDrain: 1})
	w.AddSystem(SystemOrderGrowth, &GrowthSystem{TicksPerStage: cfg.GrowthTicks})
	w.AddSystem(SystemOrderRespawn, &RespawnSystem{})
	w.AddSystem(SystemOrderCraft, &CraftSystem{})
	w.AddSystem(SystemOrderDeath, &DeathSystem{})
}

// DayNightSystem 昼夜推进（order 10）：推进 Resource.DayCycle。
type DayNightSystem struct{}

// dayLengthTicks：一个完整昼夜的 tick 数。
// 世界 tick 为 50ms（20Hz）时，4800 tick = 4 分钟一圈；
// 保持昼夜真实时长不随 tick 变化。
const dayLengthTicks = 4800

func (s *DayNightSystem) Update(w *ecs.World, dt time.Duration) {
	dc := ecs.Resource[components.DayCycle](w)
	dc.Phase++
	// 简化光照：确定性整数运算，0..1 循环
	dc.Light = float32(dc.Phase%dayLengthTicks) / dayLengthTicks
}

// HungerSystem 饥饿消耗（order 100）：有 Hunger 的实体每 tick 按组件 Rate 扣减。
// Rate <= 0 表示不消耗（调试默认）；不同角色可设不同 Rate。
type HungerSystem struct{}

func (s *HungerSystem) Update(w *ecs.World, dt time.Duration) {
	ecs.Query[components.Hunger](w, func(e ecs.Entity, h *components.Hunger) {
		if h.Level <= 0 || h.Rate <= 0 {
			return
		}
		h.Level -= h.Rate
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

// RespawnSystem 重生（order 115）：可重生（Respawnable）实体创建时就挂载，
// 耗尽后自动开始倒计时（TicksLeft），到点恢复受激能力的 WorkLeft（如浆果丛重新长出）。
// 全生命周期在本系统闭环，work 不再手动挂重生标记。
type RespawnSystem struct{}

func (s *RespawnSystem) Update(w *ecs.World, dt time.Duration) {
	var due []ecs.Entity
	ecs.Query[components.Respawnable](w, func(e ecs.Entity, r *components.Respawnable) {
		if !interactive.WorkDepleted(w, e) {
			if r.TicksLeft != 0 {
				r.TicksLeft = 0
				ecs.MarkDirty[components.Respawnable](w, e)
			}
			return
		}
		if r.Ticks <= 0 {
			return
		}
		if r.TicksLeft <= 0 {
			r.TicksLeft = r.Ticks
		} else {
			r.TicksLeft--
		}
		if r.TicksLeft <= 0 {
			due = append(due, e)
		}
		ecs.MarkDirty[components.Respawnable](w, e)
	})
	for _, e := range due {
		interactive.RestoreWork(w, e)
		r := ecs.Get[components.Respawnable](w, e)
		r.TicksLeft = 0
		ecs.MarkDirty[components.Respawnable](w, e)
	}
}

// CraftSystem 制作倒计时（order 120）：带 Crafting 组件的实体每 tick 递减，
// 到点后由世界（completeCrafts）产出并移除（产出逻辑需要配方表，留在 world 层）。
type CraftSystem struct{}

func (s *CraftSystem) Update(w *ecs.World, dt time.Duration) {
	ecs.Query[components.Crafting](w, func(e ecs.Entity, c *components.Crafting) {
		if c.TicksLeft <= 0 {
			return
		}
		c.TicksLeft--
		ecs.MarkDirty[components.Crafting](w, e)
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
