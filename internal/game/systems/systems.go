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
	GrowthTicks int // 可生长实体每多少 tick 长一阶段
}

// SystemOrder 系统固定顺序（规划文档 §7：order 冲突报错，阶段间留间隔）。
const (
	SystemOrderDayNight   = 10
	SystemOrderHunger     = 100
	SystemOrderStarvation = 105
	SystemOrderGrowth     = 110
	SystemOrderRespawn    = 115
	SystemOrderDeath      = 130
)

// RegisterAll 注册全部玩法系统（固定顺序）。
// 系统数量增长时按域拆分到子文件（如 hunger.go / death.go），在此统一装配。
func RegisterAll(w *ecs.World, cfg Config) {
	if cfg.GrowthTicks <= 0 {
		cfg.GrowthTicks = 20
	}
	w.AddSystem(SystemOrderDayNight, &DayNightSystem{})
	w.AddSystem(SystemOrderHunger, &HungerSystem{})
	w.AddSystem(SystemOrderStarvation, &StarvationSystem{HealthDrain: 1})
	w.AddSystem(SystemOrderGrowth, &GrowthSystem{TicksPerStage: cfg.GrowthTicks})
	w.AddSystem(SystemOrderRespawn, &RespawnSystem{})
	w.AddSystem(SystemOrderDeath, &DeathSystem{})
}

// DayNightSystem 昼夜推进（order 10）：推进 Resource.DayCycle。
type DayNightSystem struct{}

// dayLengthTicks：一个完整昼夜的 tick 数。
// 世界 tick 为 100ms，24 tick ≈ 2.4s（太快，页面看起来在“闪烁”）；
// 2400 tick = 4 分钟一圈，既能直观看到昼夜，也不会刺眼。
const dayLengthTicks = 2400

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

// RespawnSystem 重生（order 115）：带 Respawn 组件的实体倒计时，
// 到点恢复 Workable.WorkLeft（如浆果丛重新长出）并移除标记。
// 有 Respawn 组件 = 可重生（由命令层在耗尽时按模板 respawn_ticks 挂上）。
type RespawnSystem struct{}

func (s *RespawnSystem) Update(w *ecs.World, dt time.Duration) {
	var due []ecs.Entity
	ecs.Query[components.Respawn](w, func(e ecs.Entity, r *components.Respawn) {
		r.Ticks--
		if r.Ticks <= 0 {
			due = append(due, e)
		}
	})
	for _, e := range due {
		if ecs.Has[components.Workable](w, e) {
			wk := ecs.Get[components.Workable](w, e)
			wk.WorkLeft = wk.MaxWork
			ecs.MarkDirty[components.Workable](w, e)
		}
		ecs.Remove[components.Respawn](w, e)
	}
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
