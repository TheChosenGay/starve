// ecsdemo 是一个可运行的完整 ECS 流程示例：
// 组件 → 资源 → 系统（固定顺序）→ tick 循环 → 事件/dirty 快照 → 实体 ID 复用。
//
// 运行：go run ./cmd/ecsdemo
package main

import (
	"fmt"
	"time"

	"starve/internal/ecs"
)

// ---- 组件：纯数据，零注册仪式，直接 Add ----
type Position struct{ X, Y int }
type Health struct{ Max, Cur int }
type Hunger struct{ Level int }
type Growable struct{ Stage int }

// ---- 资源：种子化 RNG（L2 确定性）----
type rng struct{ seed uint64 }

func (r *rng) Next() uint64 {
	r.seed = r.seed*6364136223846793005 + 1442695040888963407
	return r.seed
}

// ---- 系统：固定顺序（阶段常量，不是魔法数字）----
const (
	orderSim    = 100 // 模拟
	orderGrowth = 110
	orderDeath  = 120 // 结算
)

type hungerSystem struct{ rate int }

func (s *hungerSystem) Update(w *ecs.World, dt time.Duration) {
	ecs.Query[Hunger](w, func(e ecs.Entity, h *Hunger) {
		h.Level -= s.rate // 固定 dt 下直接按 tick 计
	})
}

type growthSystem struct{}

func (s *growthSystem) Update(w *ecs.World, dt time.Duration) {
	r := ecs.Resource[rng](w) // 随机数一律从 Resource 取（§4.4）
	ecs.Query2[Growable, Health](w, func(e ecs.Entity, g *Growable, hp *Health) {
		if r.Next()%2 == 0 && g.Stage < 3 {
			g.Stage++
			w.MarkDirty(e, Growable{}) // 指针直改后手动标记 dirty
		}
	})
}

type deathSystem struct{}

func (s *deathSystem) Update(w *ecs.World, dt time.Duration) {
	// 饿死的实体收集后统一销毁（不能在 Query 回调里直接 Destroy，会破坏遍历）
	var dead []ecs.Entity
	ecs.Query[Hunger](w, func(e ecs.Entity, h *Hunger) {
		if h.Level <= 0 {
			dead = append(dead, e)
		}
	})
	for _, e := range dead {
		w.DestroyEntity(e)
	}
}

func main() {
	w := ecs.NewWorld()

	// 资源：每个世界一个独立种子 RNG
	w.AddResource(&rng{seed: 42})

	// 系统：固定顺序注册
	w.AddSystem(orderSim, &hungerSystem{rate: 7})
	w.AddSystem(orderGrowth, &growthSystem{})
	w.AddSystem(orderDeath, &deathSystem{})

	// 实体：组件直接挂，不需要任何注册仪式
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 3, Y: 5})
	ecs.Add(w, player, Health{Max: 100, Cur: 100})
	ecs.Add(w, player, Hunger{Level: 50})

	tree := w.CreateEntity()
	ecs.Add(w, tree, Position{X: 8, Y: 2})
	ecs.Add(w, tree, Health{Max: 50, Cur: 50})
	ecs.Add(w, tree, Growable{Stage: 0})

	pig := w.CreateEntity()
	ecs.Add(w, pig, Position{X: 10, Y: 4})
	ecs.Add(w, pig, Health{Max: 60, Cur: 60})
	ecs.Add(w, pig, Hunger{Level: 30})

	fire := w.CreateEntity()
	ecs.Add(w, fire, Position{X: 6, Y: 6})

	// 组件名默认 = Go 类型名；要改名/挂编解码器，在第一次 Add/Query 前
	// 调用 ecs.RegisterComponent[T](w, "Name", codec)。

	// 初始化阶段的事件/dirty 先清掉并展示一次（M3 里由 actor 在 tick 边界 drain）
	fmt.Printf("初始化：%d 个实体\n", w.EntityCount())
	printEvents(w.DrainEvents())
	for _, d := range w.DrainDirtySorted() {
		fmt.Printf("  dirty entity=%d comps=%v\n", d.Entity, d.Comps)
	}
	fmt.Println()

	const dt = 100 * time.Millisecond // 10Hz 固定步长（M3 起由 actor 注入）
	for tick := 1; tick <= 6; tick++ {
		// ① 输入：玩家意图（M3 里先入命令缓冲，tick 统一消费）
		if tick == 2 {
			ecs.Set(w, player, Position{X: 4, Y: 5}) // Set = 写 + 自动 mark dirty
		}

		// ② 模拟：固定 dt 跑系统
		w.RunSystems(dt)

		// ③ 输出：事件 + dirty 快照
		fmt.Printf("=== tick %d（世界时钟 %dms）===\n", tick, tick*100)
		printEvents(w.DrainEvents())
		for _, d := range w.DrainDirtySorted() {
			fmt.Printf("  dirty entity=%d comps=%v\n", d.Entity, d.Comps)
		}
		dump(w)
		fmt.Println()
	}

	// 实体 ID 复用：销毁 → 新建拿到同一个 ID
	w.DestroyEntity(fire)
	reused := w.CreateEntity()
	fmt.Printf("销毁篝火(%d) 后新建实体 = %d（空闲列表复用 ID）\n", fire, reused)
}

func printEvents(evs []ecs.Event) {
	for _, ev := range evs {
		line := fmt.Sprintf("  事件 %s entity=%d", ev.Kind, ev.Entity)
		if ev.Component != "" {
			line += fmt.Sprintf(" comp=%s", ev.Component)
		}
		fmt.Println(line)
	}
}

// dump 用 Query2 把"同时有 Position 和 Health"的实体打出来（篝火只有 Position，会被跳过）
func dump(w *ecs.World) {
	ecs.Query2[Position, Health](w, func(e ecs.Entity, p *Position, hp *Health) {
		line := fmt.Sprintf("  实体 %d @(%d,%d) HP %d/%d", e, p.X, p.Y, hp.Cur, hp.Max)
		if ecs.Has[Hunger](w, e) {
			line += fmt.Sprintf(" 饥饿 %d", ecs.Get[Hunger](w, e).Level)
		}
		if ecs.Has[Growable](w, e) {
			line += fmt.Sprintf(" 树阶段 %d", ecs.Get[Growable](w, e).Stage)
		}
		fmt.Println(line)
	})
}
