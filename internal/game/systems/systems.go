// Package systems 存放玩法系统（M5）。
// 约束（规划文档 §7）：纯函数——不调 actor API、不发网络消息；
// 指针直改组件后手动 MarkDirty，变更才会进增量快照。
package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// DayNightSystem 昼夜推进（order 10）：推进 Resource.DayCycle。
type DayNightSystem struct{}

func (s *DayNightSystem) Update(w *ecs.World, dt time.Duration) {
	dc := ecs.Resource[components.DayCycle](w)
	dc.Phase++
	// 简化光照：24 阶段循环，确定性整数运算
	dc.Light = float32(dc.Phase%24) / 24
}

// HungerSystem 饥饿消耗（order 100）：有 Hunger 的实体每 tick 扣速率。
type HungerSystem struct {
	Rate int
}

func (s *HungerSystem) Update(w *ecs.World, dt time.Duration) {
	ecs.Query[components.Hunger](w, func(e ecs.Entity, h *components.Hunger) {
		if h.Level <= 0 {
			return
		}
		h.Level -= s.Rate
		if h.Level < 0 {
			h.Level = 0
		}
		w.MarkDirty(e, components.Hunger{})
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
			w.MarkDirty(e, components.Growable{})
		}
	})
}

// DeathSystem 死亡结算（order 130）：Health<=0 或 Hunger<=0 的实体销毁。
// 销毁会触发 dirty（removed）与 EntityDestroyed 事件，进入增量快照。
type DeathSystem struct{}

func (s *DeathSystem) Update(w *ecs.World, dt time.Duration) {
	var dead []ecs.Entity
	ecs.Query[components.Health](w, func(e ecs.Entity, hp *components.Health) {
		if hp.Cur <= 0 {
			dead = append(dead, e)
		}
	})
	ecs.Query[components.Hunger](w, func(e ecs.Entity, h *components.Hunger) {
		if h.Level <= 0 {
			dead = append(dead, e)
		}
	})
	for _, e := range dead {
		if w.IsAlive(e) {
			w.DestroyEntity(e)
		}
	}
}
