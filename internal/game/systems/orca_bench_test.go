package systems

import (
	"fmt"
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// ORCA 与移动求解的性能基准。
//
// 关心的预算：服务端一个 tick 是 50ms（20Hz）。MoveSystem 要为**每个**移动实体
// 跑一次三阶段求解，所以单次成本 × 实体数必须远小于 50ms。
//
// 运行：go test -bench=BenchmarkORCA -benchmem -run '^$' ./internal/game/systems/

// benchNeighbors 造 n 个邻居，围在半径 ring 的圆上（等角分布）。
func benchNeighbors(n int, ring float64) []ORCABody {
	out := make([]ORCABody, n)
	for i := range out {
		ang := 2 * math.Pi * float64(i) / float64(n)
		out[i] = ORCABody{
			X: math.Cos(ang) * ring, Z: math.Sin(ang) * ring,
			VX: -math.Cos(ang), VY: -math.Sin(ang), // 都朝圆心走（最坏情形）
			Radius: 0.3, MaxSpeed: 10,
		}
	}
	return out
}

// BenchmarkORCASolve 单次 ORCA 求解，随邻居数变化。
func BenchmarkORCASolve(b *testing.B) {
	solver := NewORCASolverFor(DefaultORCAOptions(), 1)
	self := Agent{VX: 10, VY: 0, PrefVX: 10, PrefVY: 0, X: 0, Z: 0, Radius: 0.305, MaxSpeed: 10}
	for _, n := range []int{0, 4, 16, 64} {
		ns := benchNeighbors(n, 3)
		b.Run(fmt.Sprintf("neighbors=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				solver.Solve(self, ns)
			}
		})
	}
}

// BenchmarkORCASolveCrowded 拥挤情形：所有人挤在很近的圈上（可行域最小、求解最贵）。
func BenchmarkORCASolveCrowded(b *testing.B) {
	solver := NewORCASolverFor(DefaultORCAOptions(), 1)
	self := Agent{VX: 10, VY: 0, PrefVX: 10, PrefVY: 0, X: 0, Z: 0, Radius: 0.305, MaxSpeed: 10}
	for _, n := range []int{16, 64} {
		ns := benchNeighbors(n, 0.8) // 半径 0.8 的密圈：严重重叠
		b.Run(fmt.Sprintf("crowded=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				solver.Solve(self, ns)
			}
		})
	}
}

// BenchmarkNeighborsQuery 邻居查询（宽阶段 BVH）：随世界动态体总数变化。
func BenchmarkNeighborsQuery(b *testing.B) {
	for _, total := range []int{100, 500, 2000} {
		idx := newBenchIndex(total)
		b.Run(fmt.Sprintf("total=%d", total), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				idx.Neighbors(50, 50, 20, 1)
			}
		})
	}
}

// BenchmarkMoveSolverFull 完整三阶段求解（静态滑掠 + 邻居查询 + ORCA）。
func BenchmarkMoveSolverFull(b *testing.B) {
	for _, total := range []int{50, 200, 1000} {
		world := newBenchWorld(total)
		solver := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 0)
		b.Run(fmt.Sprintf("movers=%d", total), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				solveAllMovers(b, world, solver)
			}
		})
	}
}

// newBenchIndex 建一个含 total 个动态体 + 一些静态障碍的索引。
func newBenchIndex(total int) *collision.Index {
	idx := collision.NewIndex()
	// 静态障碍：均匀散布（模拟森林）
	for i := 0; i < 500; i++ {
		x := float64((i%25)*4) + 2
		y := float64((i/25)*4) + 2
		idx.Set(ecs.Entity(1_000_000+i), x, y, 0.18)
	}
	// 动态体：散在地图上
	side := int(math.Ceil(math.Sqrt(float64(total))))
	for i := 0; i < total; i++ {
		x := float64(i%side)*2 + 1
		y := float64(i/side)*2 + 1
		idx.SetDynamic(ecs.Entity(i+1), x, y, 0.305, 0, 0, 0)
	}
	return idx
}

// newBenchWorld 建一个含 total 个移动实体的 ECS 世界（含静态障碍与索引）。
func newBenchWorld(total int) *ecs.World {
	w := ecs.NewWorld()
	RegisterAll(w, Config{})
	idx := collision.NewIndex()
	w.AddResource(idx)
	// 静态
	for i := 0; i < 500; i++ {
		e := w.CreateEntity()
		ecs.Add(w, e, components.Position{X: (i % 25) * 4, Y: (i / 25) * 4})
		ecs.Add(w, e, components.Static{})
		ecs.Add(w, e, components.Collide{Shape: components.CollideShapeCircle, Radius: 0.18})
		idx.Set(e, float64((i%25)*4)+2, float64((i/25)*4)+2, 0.18)
	}
	// 动态
	side := int(math.Ceil(math.Sqrt(float64(total))))
	for i := 0; i < total; i++ {
		e := w.CreateEntity()
		x := i%side*2 + 1
		y := i/side*2 + 1
		ecs.Add(w, e, components.Position{X: x, Y: y})
		ecs.Add(w, e, components.Moveable{Speed: 10, DirX: 1, VelX: 10})
		ecs.Add(w, e, components.Dynamic{})
		ecs.Add(w, e, components.Collide{Shape: components.CollideShapeCapsule, Radius: 0.305})
		idx.SetDynamic(e, float64(x), float64(y), 0.305, 0, 0, 0)
	}
	return w
}

// solveAllMovers 对世界里所有移动实体跑一次三阶段求解（模拟一个 tick 的 MoveSystem 求解部分）。
func solveAllMovers(b *testing.B, w *ecs.World, solver *MoveSolver) {
	b.Helper()
	ents := SortMoveEntities(w)
	for _, e := range ents {
		mv := ecs.Get[components.Moveable](w, e)
		p := ecs.Get[components.Position](w, e)
		var col *components.Collide
		if ecs.Has[components.Collide](w, e) {
			col = ecs.Get[components.Collide](w, e)
		}
		_ = solver.Solve(w, e, p, mv, col, MoveInput{
			DesiredX: 0.5, DesiredY: 0, DirX: 1, DirY: 0, Speed: 10, DT: 0.05,
		})
	}
}
