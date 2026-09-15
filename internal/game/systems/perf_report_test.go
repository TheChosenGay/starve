package systems

import (
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// 移动求解的性能基准与对比报告。
//
// 跑法：go test -run TestPerfReport -v ./internal/game/systems/
//
// 关注的量：
//   - tick 总耗时（预算 50ms = 20Hz）
//   - 各阶段占比（desired / slide / avoid）
//   - 两种邻居来源（BVH vs OrcaAOI）的差异
//   - 耗时随实体数与邻居数的缩放

// perfScene 是一组"实体数 + 间距"的场景。
type perfScene struct {
	Name    string
	Total   int
	Spacing int
}

func perfScenes() []perfScene {
	return []perfScene{
		{"200 实体·间距4", 200, 4},
		{"1000 实体·间距4", 1000, 4},
		{"2000 实体·间距4", 2000, 4},
		{"1000 实体·间距2(密)", 1000, 2},
		{"4000 实体·间距4", 4000, 4},
	}
}

// perfWorld 造一个场景世界：静态障碍 + 规则分布的移动体。
func perfWorld(s perfScene) (*ecs.World, *collision.Index, []ecs.Entity) {
	w := ecs.NewWorld()
	RegisterAll(w, Config{})
	idx := collision.NewIndex()
	w.AddResource(idx)
	// 静态障碍：每 3 格一棵，模拟森林
	for i := 0; i < 2000; i++ {
		e := w.CreateEntity()
		x := float64((i%45)*3) + 0.5
		y := float64((i/45)*3) + 0.5
		idx.Set(e, x, y, 0.18)
	}
	side := int(math.Ceil(math.Sqrt(float64(s.Total))))
	movers := make([]ecs.Entity, 0, s.Total)
	for i := 0; i < s.Total; i++ {
		e := w.CreateEntity()
		x := i%side*s.Spacing + 5
		y := i/side*s.Spacing + 5
		ecs.Add(w, e, components.Position{X: x, Y: y})
		ecs.Add(w, e, components.Moveable{Speed: 10, DirX: 1, VelX: 10})
		ecs.Add(w, e, components.Collide{Shape: components.CollideShapeCapsule, Radius: 0.305})
		idx.SetDynamic(e, float64(x), float64(y), 0.305, 0, 0, 0)
		movers = append(movers, e)
	}
	return w, idx, movers
}

// medianTick 跑 rounds 轮、每轮 ticks 个 tick，返回中位数耗时（ms/tick）。
func medianTick(w *ecs.World, movers []ecs.Entity, solver *MoveSolver, rounds, ticks int) float64 {
	dtSec := 0.05
	samples := make([]float64, 0, rounds)
	for r := 0; r < rounds; r++ {
		start := time.Now()
		for tk := 0; tk < ticks; tk++ {
			for _, e := range movers {
				p := ecs.Get[components.Position](w, e)
				mv := ecs.Get[components.Moveable](w, e)
				col := ecs.Get[components.Collide](w, e)
				solver.Solve(w, e, p, mv, col, MoveInput{
					DesiredX: 0.5, DirX: 1, Speed: 10, DT: dtSec,
				})
			}
		}
		samples = append(samples, float64(time.Since(start).Nanoseconds())/float64(ticks)/1e6)
	}
	sort.Float64s(samples)
	return samples[len(samples)/2]
}

// avgNeighbors 统计平均邻居数。
func avgNeighbors(w *ecs.World, idx *collision.Index, movers []ecs.Entity, radius float64) float64 {
	total := 0
	for _, e := range movers {
		p := ecs.Get[components.Position](w, e)
		total += len(idx.Neighbors(float64(p.X)+0.5, float64(p.Y)+0.5, radius, e))
	}
	return float64(total) / float64(len(movers))
}

// TestPerfReport 输出完整性能报告。
func TestPerfReport(t *testing.T) {
	if testing.Short() {
		t.Skip("skip in -short")
	}
	const rounds, ticks = 5, 10

	fmt.Println()
	fmt.Println("════════ 移动求解性能报告 ════════")
	fmt.Println()

	// ── 表 1：各场景 tick 耗时（两种邻居来源）──
	fmt.Println("【表 1】整 tick 耗时（1000 移动体含静态滑掠 + ORCA；预算 50ms）")
	fmt.Printf("%-22s %8s %10s %10s %8s\n", "场景", "平均邻居", "BVH", "OrcaAOI", "省")
	for _, sc := range perfScenes() {
		w, idx, movers := perfWorld(sc)

		bvh := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 0)
		bvhT := medianTick(w, movers, bvh, rounds, ticks)

		w2, idx2, movers2 := perfWorld(sc)
		aoi := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 0)
		aoi.EnableGrid(256, 256)
		// OrcaAOI 需要每 tick 先同步（真实路径由 MoveSystem 调用）
		aoiT := medianTickWithSync(w2, movers2, aoi, rounds, ticks)

		avg := avgNeighbors(w, idx, movers, bvh.neighborRadius())
		pct := (bvhT - aoiT) / bvhT * 100
		fmt.Printf("%-22s %8.1f %8.2fms %8.2fms %7.0f%%\n", sc.Name, avg, bvhT, aoiT, pct)
		_ = idx2
	}

	// ── 表 2：阶段占比 ──
	fmt.Println()
	fmt.Println("【表 2】1000 实体各阶段占比")
	{
		sc := perfScene{"1000", 1000, 4}
		w, idx, movers := perfWorld(sc)
		solver := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 0)

		// desired only
		start := time.Now()
		for r := 0; r < rounds; r++ {
			for tk := 0; tk < ticks; tk++ {
				for _, e := range movers {
					p := ecs.Get[components.Position](w, e)
					_, _ = DesiredDisplacement(w, components.MoveDir{DX: 1}, 10, 0.05,
						float64(p.X)+0.5, float64(p.Y)+0.5)
				}
			}
		}
		desired := float64(time.Since(start).Nanoseconds()) / rounds / ticks / 1e6

		// +slide
		start = time.Now()
		for r := 0; r < rounds; r++ {
			for tk := 0; tk < ticks; tk++ {
				for _, e := range movers {
					p := ecs.Get[components.Position](w, e)
					col := ecs.Get[components.Collide](w, e)
					b := BodyOf(float64(p.X)+0.5, float64(p.Y)+0.5, col)
					idx.SlideStatic(b, 0.5, 0)
				}
			}
		}
		slide := float64(time.Since(start).Nanoseconds())/rounds/ticks/1e6 - desired

		// +avoid（完整）
		full := medianTick(w, movers, solver, rounds, ticks)
		avoid := full - desired - slide

		fmt.Printf("  ① desired（意图位移）      %6.3f ms  %5.1f%%\n", desired, desired/full*100)
		fmt.Printf("  ② slide  （静态滑掠）      %6.3f ms  %5.1f%%\n", slide, slide/full*100)
		fmt.Printf("  ③ avoid  （邻居+ORCA+提交）%6.3f ms  %5.1f%%\n", avoid, avoid/full*100)
		fmt.Printf("  ── 合计                   %6.3f ms\n", full)
	}

	// ── 表 3：随实体数缩放（固定密度）──
	fmt.Println()
	fmt.Println("【表 3】固定密度（间距 4）扩数量——验证线性缩放")
	fmt.Printf("%-12s %8s %12s %12s\n", "实体数", "平均邻居", "耗时", "每实体")
	for _, n := range []int{250, 1000, 4000} {
		sc := perfScene{fmt.Sprint(n), n, 4}
		w, idx, movers := perfWorld(sc)
		solver := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 0)
		ms := medianTick(w, movers, solver, rounds, ticks)
		avg := avgNeighbors(w, idx, movers, solver.neighborRadius())
		fmt.Printf("%-12d %8.1f %10.2fms %10.2fµs\n", n, avg, ms, ms*1000/float64(n))
	}

	// ── 表 4：ORCA 单次求解（纯计算，无 ECS）──
	fmt.Println()
	fmt.Println("【表 4】ORCA 单次求解（纯数学，0 分配）")
	fmt.Printf("%-14s %12s\n", "邻居数", "单次耗时")
	orca := NewORCASolver(DefaultORCAOptions())
	self := Agent{X: 0, Z: 0, VX: 10, PrefVX: 10, Radius: 0.305, MaxSpeed: 10}
	for _, n := range []int{0, 4, 16, 64} {
		bodies := make([]ORCABody, n)
		for i := range bodies {
			bodies[i] = ORCABody{X: float64(i) * 1.5, Z: float64(i % 3), VX: -1, Radius: 0.3, MaxSpeed: 10}
		}
		const iters = 200000
		start := time.Now()
		for i := 0; i < iters; i++ {
			orca.Solve(self, bodies)
		}
		ns := time.Since(start).Nanoseconds() / iters
		fmt.Printf("%-14d %10d ns\n", n, ns)
	}
	fmt.Println()
}

// medianTickWithSync 与 medianTick 相同，但每 tick 先同步 OrcaAOI
// （真实路径里由 MoveSystem 调用 SyncGrid）。
func medianTickWithSync(w *ecs.World, movers []ecs.Entity, solver *MoveSolver, rounds, ticks int) float64 {
	samples := make([]float64, 0, rounds)
	for r := 0; r < rounds; r++ {
		start := time.Now()
		for tk := 0; tk < ticks; tk++ {
			solver.SyncGrid(w)
			solver.RefreshNeighborCache()
			for _, e := range movers {
				p := ecs.Get[components.Position](w, e)
				mv := ecs.Get[components.Moveable](w, e)
				col := ecs.Get[components.Collide](w, e)
				solver.Solve(w, e, p, mv, col, MoveInput{
					DesiredX: 0.5, DirX: 1, Speed: 10, DT: 0.05,
				})
			}
		}
		samples = append(samples, float64(time.Since(start).Nanoseconds())/float64(ticks)/1e6)
	}
	sort.Float64s(samples)
	return samples[len(samples)/2]
}
