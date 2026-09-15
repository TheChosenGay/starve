package systems

import (
	"fmt"
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// 邻域查询方案的交叉对比基准。
//
// 目的：在**完全相同的实体分布**下，比较四种"找邻居"的实现：
//   基线  —— 现状：每实体查一次 BVH（Index.Neighbors）
//   A 降频 —— 邻居列表缓存，每 N tick 刷新一次（位置仍实时取）
//   B 网格 —— 动态体专用均匀网格，只登记 Dynamic 实体
//   C 四叉树 —— 动态体专用四叉树
//
// 关注三个维度：
//   ① 单次/整体查询耗时
//   ② 命中邻居数是否一致（正确性：方案之间必须给出同一个集合）
//   ③ 分配次数（GC 压力）

// benchLayout 描述一种实体分布。
type benchLayout struct {
	Name    string
	Total   int
	Spacing int // 网格间距（格）；越小越密
	Radius  float64
}

func defaultLayouts() []benchLayout {
	return []benchLayout{
		{"sparse_200", 200, 20, 10},
		{"medium_500", 500, 8, 10},
		{"dense_1000", 1000, 4, 10},
		{"verydense_2000", 2000, 2, 10},
	}
}

// buildScene 按布局造一个世界：静态障碍 + 动态体，并返回索引。
func buildScene(l benchLayout) (*ecs.World, *collision.Index, []ecs.Entity) {
	w := ecs.NewWorld()
	RegisterAll(w, Config{})
	idx := collision.NewIndex()
	w.AddResource(idx)

	// 静态障碍（模拟森林）：每 6 格一棵
	for i := 0; i < 300; i++ {
		e := w.CreateEntity()
		x := (i % 20) * 6
		y := (i / 20) * 6
		ecs.Add(w, e, components.Position{X: x, Y: y})
		ecs.Add(w, e, components.Collide{Shape: components.CollideShapeCircle, Radius: 0.18})
		idx.Set(e, float64(x)+0.5, float64(y)+0.5, 0.18)
	}

	side := int(math.Ceil(math.Sqrt(float64(l.Total))))
	movers := make([]ecs.Entity, 0, l.Total)
	for i := 0; i < l.Total; i++ {
		e := w.CreateEntity()
		x := i%side*l.Spacing + 1
		y := i/side*l.Spacing + 1
		ecs.Add(w, e, components.Position{X: x, Y: y})
		ecs.Add(w, e, components.Moveable{Speed: 10, DirX: 1, VelX: 10})
		ecs.Add(w, e, components.Collide{
			Shape: components.CollideShapeCapsule, Radius: 0.305,
		})
		idx.SetDynamic(e, float64(x), float64(y), 0.305, 0, 0, 0)
		movers = append(movers, e)
	}
	return w, idx, movers
}

// moverPos 取实体的连续位置（格）。
func moverPos(w *ecs.World, e ecs.Entity) (float64, float64) {
	p := ecs.Get[components.Position](w, e)
	mv := ecs.Get[components.Moveable](w, e)
	if mv == nil {
		return float64(p.X), float64(p.Y)
	}
	return float64(p.X) + mv.SubX, float64(p.Y) + mv.SubY
}

// sumNeighborsBaseline 用现状（BVH 逐实体查询）统计邻居数总和，
// 顺便返回每个实体的邻居数（用于各方案之间的一致性对比）。
func sumNeighborsBaseline(w *ecs.World, idx *collision.Index, movers []ecs.Entity, radius float64) []int {
	counts := make([]int, len(movers))
	for i, e := range movers {
		x, z := moverPos(w, e)
		counts[i] = len(idx.Neighbors(x, z, radius, e))
	}
	return counts
}

// measureLayout 打印一种布局下的基线数据。
func measureLayout(t *testing.T, l benchLayout) {
	t.Helper()
	w, idx, movers := buildScene(l)
	counts := sumNeighborsBaseline(w, idx, movers, l.Radius)
	total := 0
	for _, c := range counts {
		total += c
	}
	per := float64(total) / float64(len(movers))
	t.Logf("%-16s 实体=%4d 间距=%2d 半径=%.0f -> 平均邻居 %.1f 总计 %d",
		l.Name, l.Total, l.Spacing, l.Radius, per, total)
	_ = fmt.Sprint
}

// TestNeighborBaseline 打印各布局下的基线（现状 BVH）邻居统计。
// 这是后面三个方案对比的"标尺"：邻居集合必须一致。
func TestNeighborBaseline(t *testing.T) {
	for _, l := range defaultLayouts() {
		measureLayout(t, l)
	}
}

// BenchmarkNeighborBaseline 基线性能：每 tick 为所有实体查一次 BVH。
func BenchmarkNeighborBaseline(b *testing.B) {
	for _, l := range defaultLayouts() {
		w, idx, movers := buildScene(l)
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sumNeighborsBaseline(w, idx, movers, l.Radius)
			}
		})
	}
}

// ---- 方案 A：降频缓存 ----

// sumNeighborsCached 模拟降频：每 every tick 刷新一次邻居表，
// 非刷新 tick 直接用缓存（只读 id，位置实时取）。
// 返回 (累计邻居数, 查询调用次数)。
func sumNeighborsCached(
	w *ecs.World, idx *collision.Index, movers []ecs.Entity, radius float64,
	cacher *neighborCacher,
) (int, int) {
	refresh := cacher.shouldRefresh()
	if refresh {
		cacher.beginRefresh()
	}
	total := 0
	queries := 0
	for _, e := range movers {
		if refresh {
			x, z := moverPos(w, e)
			ns := idx.Neighbors(x, z, radius, e)
			cacher.put(e, ns)
			queries++
			total += len(ns)
			continue
		}
		total += len(cacher.get(e))
	}
	return total, queries
}

// BenchmarkNeighborCached 降频方案：每 4 tick 刷新一次（跳过的 tick 用缓存）。
// 基准里按"平均每次调用"摊算：4 次里 1 次真查、3 次读缓存。
func BenchmarkNeighborCached(b *testing.B) {
	for _, l := range defaultLayouts() {
		w, idx, movers := buildScene(l)
		cacher := newNeighborCacher(4)
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sumNeighborsCached(w, idx, movers, l.Radius, cacher)
			}
		})
	}
}

// TestNeighborCachedSameSet 降频不能改变"邻居是谁"——只延后知道的时间。
// 静止场景下，刷新 tick 与缓存 tick 必须给出同一个集合。
func TestNeighborCachedSameSet(t *testing.T) {
	l := defaultLayouts()[2] // dense_1000
	w, idx, movers := buildScene(l)
	base := sumNeighborsBaseline(w, idx, movers, l.Radius)

	cacher := newNeighborCacher(4)
	// 第 1 次调用是刷新 tick：集合应与基线一致
	got1, q1 := sumNeighborsCached(w, idx, movers, l.Radius, cacher)
	// 第 2 次调用是非刷新 tick：集合应仍与基线一致（实体没动）
	got2, q2 := sumNeighborsCached(w, idx, movers, l.Radius, cacher)

	want := 0
	for _, c := range base {
		want += c
	}
	if got1 != want || got2 != want {
		t.Fatalf("降频改变了邻居集合：基线 %d, 刷新 %d, 缓存 %d", want, got1, got2)
	}
	t.Logf("dense_1000: 基线总计=%d 刷新tick=%d(查询%d次) 缓存tick=%d(查询%d次)",
		want, got1, q1, got2, q2)
}

// ---- 方案 B：动态体均匀网格 ----

// buildGrid 按当前实体位置建网格（每 tick 一次：Reset + 全量 Add）。
func buildGrid(w *ecs.World, movers []ecs.Entity, gw, gh int) *collision.OrcaAOI {
	g := collision.NewOrcaAOI(gw, gh, 1)
	g.Reset()
	for _, e := range movers {
		x, z := moverPos(w, e)
		g.Add(e, x, z)
	}
	return g
}

// shapeOfEntity 返回网格查询用的形状访问器（半径 + 胶囊半长），
// 与服务端 collectNeighbors 的语义一致。
func shapeOfEntity(w *ecs.World) collision.ShapeOf {
	return func(e ecs.Entity) (float64, float64) {
		if ecs.Has[components.Collide](w, e) {
			c := ecs.Get[components.Collide](w, e)
			return c.Radius, c.HalfLength
		}
		return 0.3, 0
	}
}

// sumNeighborsGrid 用网格收集所有实体的邻居，返回总数与逐实体计数。
func sumNeighborsGrid(w *ecs.World, movers []ecs.Entity, radius float64) (int, []int) {
	g := buildGrid(w, movers, 200, 200)
	counts := make([]int, len(movers))
	total := 0
	var buf []collision.Neighbor
	for i, e := range movers {
		x, z := moverPos(w, e)
		ns := g.Neighbors(x, z, radius, e,
			shapeOfEntity(w), buf)
		buf = ns[:0]
		counts[i] = len(ns)
		total += len(ns)
	}
	return total, counts
}

// TestNeighborGridSameSet 方案 B 必须给出与基线**完全相同**的邻居集合。
func TestNeighborGridSameSet(t *testing.T) {
	for _, l := range defaultLayouts() {
		w, idx, movers := buildScene(l)
		base := sumNeighborsBaseline(w, idx, movers, l.Radius)
		total, counts := sumNeighborsGrid(w, movers, l.Radius)
		want := 0
		for _, c := range base {
			want += c
		}
		if total != want {
			t.Errorf("%s: 邻居总数不一致 网格=%d 基线=%d", l.Name, total, want)
			continue
		}
		for i := range counts {
			if counts[i] != base[i] {
				t.Fatalf("%s: 实体 %d 邻居数不一致 网格=%d 基线=%d",
					l.Name, movers[i], counts[i], base[i])
			}
		}
		t.Logf("%-16s 网格与基线一致（总计 %d）", l.Name, total)
	}
}

// BenchmarkNeighborGrid 方案 B 性能。
func BenchmarkNeighborGrid(b *testing.B) {
	for _, l := range defaultLayouts() {
		w, _, movers := buildScene(l)
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sumNeighborsGrid(w, movers, l.Radius)
			}
		})
	}
}

// ---- 方案 C：动态体四叉树 ----

// buildQuadTree 按当前实体位置重建四叉树。
func buildQuadTree(w *ecs.World, movers []ecs.Entity, gw, gh int) *collision.QuadTree {
	q := collision.NewQuadTree(gw, gh)
	q.Reset()
	for _, e := range movers {
		x, z := moverPos(w, e)
		q.Add(e, x, z)
	}
	return q
}

// sumNeighborsQuad 用四叉树收集所有实体的邻居。
func sumNeighborsQuad(w *ecs.World, movers []ecs.Entity, radius float64) (int, []int, int) {
	q := buildQuadTree(w, movers, 200, 200)
	counts := make([]int, len(movers))
	total := 0
	var buf []collision.Neighbor
	for i, e := range movers {
		x, z := moverPos(w, e)
		ns := q.Neighbors(x, z, radius, e,
			shapeOfEntity(w), buf)
		buf = ns[:0]
		counts[i] = len(ns)
		total += len(ns)
	}
	return total, counts, q.NodeCount()
}

// TestNeighborQuadSameSet 方案 C 必须给出与基线完全相同的邻居集合。
func TestNeighborQuadSameSet(t *testing.T) {
	for _, l := range defaultLayouts() {
		w, idx, movers := buildScene(l)
		base := sumNeighborsBaseline(w, idx, movers, l.Radius)
		total, counts, nodes := sumNeighborsQuad(w, movers, l.Radius)
		want := 0
		for _, c := range base {
			want += c
		}
		if total != want {
			t.Errorf("%s: 邻居总数不一致 四叉树=%d 基线=%d", l.Name, total, want)
			continue
		}
		for i := range counts {
			if counts[i] != base[i] {
				t.Fatalf("%s: 实体 %d 邻居数不一致 四叉树=%d 基线=%d",
					l.Name, movers[i], counts[i], base[i])
			}
		}
		t.Logf("%-16s 四叉树与基线一致（总计 %d，节点数 %d）", l.Name, total, nodes)
	}
}

// BenchmarkNeighborQuad 方案 C 性能。
func BenchmarkNeighborQuad(b *testing.B) {
	for _, l := range defaultLayouts() {
		w, _, movers := buildScene(l)
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sumNeighborsQuad(w, movers, l.Radius)
			}
		})
	}
}

// ---- 增量更新 vs 每 tick 重建 ----

// stepLayout 让所有实体沿 +X 走一小步（模拟一个 tick：10 格/秒 × 50ms = 0.5 格），
// 并同步到 ECS 与空间索引（BVH 走 SetDynamic 增量更新）。
func stepLayout(w *ecs.World, idx *collision.Index, movers []ecs.Entity, dx float64) {
	for _, e := range movers {
		x, z := moverPos(w, e)
		x += dx
		if x > 190 {
			x = 2
		}
		nx := int(math.Floor(x))
		ecs.Set(w, e, components.Position{X: nx})
		mv := ecs.Get[components.Moveable](w, e)
		mv.SubX = x - float64(nx)
		idx.SetDynamic(e, x, z, 0.305, 0, 0, 0)
	}
}

// TestIncrementalSameSet 增量更新（走一步后）必须仍与"重建 + 查询"给出同一集合。
func TestIncrementalSameSet(t *testing.T) {
	l := defaultLayouts()[2] // dense_1000
	w, idx, movers := buildScene(l)

	// 增量维护的网格与四叉树
	g := collision.NewOrcaAOI(200, 200, 1)
	g.Reset()
	q := collision.NewQuadTree(200, 200)
	q.Reset()
	for _, e := range movers {
		x, z := moverPos(w, e)
		g.Add(e, x, z)
		q.Add(e, x, z)
	}

	for step := 0; step < 5; step++ {
		stepLayout(w, idx, movers, 0.5)
		for _, e := range movers {
			x, z := moverPos(w, e)
			g.Add(e, x, z) // 增量
			q.Add(e, x, z) // 增量
		}
	}

	base := sumNeighborsBaseline(w, idx, movers, l.Radius)
	want := 0
	for _, c := range base {
		want += c
	}

	var buf []collision.Neighbor
	gt, qt := 0, 0
	for i, e := range movers {
		x, z := moverPos(w, e)
		gns := g.Neighbors(x, z, l.Radius, e, shapeOfEntity(w), buf)
		buf = gns[:0]
		gt += len(gns)
		qns := q.Neighbors(x, z, l.Radius, e, shapeOfEntity(w), buf)
		buf = qns[:0]
		qt += len(qns)
		_ = i
	}
	if gt != want || qt != want {
		t.Fatalf("增量更新后集合不一致：基线=%d 网格=%d 四叉树=%d", want, gt, qt)
	}
	t.Logf("dense_1000 走 5 步后：基线=%d 网格(增量)=%d 四叉树(增量)=%d ✓", want, gt, qt)
}

// BenchmarkGridIncremental 网格：每 tick 只做增量更新（移动 0.5 格）+ 查询。
func BenchmarkGridIncremental(b *testing.B) {
	for _, l := range defaultLayouts() {
		w, _, movers := buildScene(l)
		g := collision.NewOrcaAOI(200, 200, 1)
		g.Reset()
		for _, e := range movers {
			x, z := moverPos(w, e)
			g.Add(e, x, z)
		}
		var buf []collision.Neighbor
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// 增量更新（0.5 格/tick，多数不跨格）
				for _, e := range movers {
					x, z := moverPos(w, e)
					g.Add(e, x, z+0.5)
				}
				for _, e := range movers {
					x, z := moverPos(w, e)
					buf = g.Neighbors(x, z+0.5, l.Radius, e,
						shapeOfEntity(w), buf)
				}
			}
		})
	}
}

// BenchmarkQuadIncremental 四叉树：每 tick 只做增量更新 + 查询。
func BenchmarkQuadIncremental(b *testing.B) {
	for _, l := range defaultLayouts() {
		w, _, movers := buildScene(l)
		q := collision.NewQuadTree(200, 200)
		q.Reset()
		for _, e := range movers {
			x, z := moverPos(w, e)
			q.Add(e, x, z)
		}
		var buf []collision.Neighbor
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, e := range movers {
					x, z := moverPos(w, e)
					q.Add(e, x, z+0.5)
				}
				for _, e := range movers {
					x, z := moverPos(w, e)
					buf = q.Neighbors(x, z+0.5, l.Radius, e,
						shapeOfEntity(w), buf)
				}
			}
		})
	}
}

// BenchmarkBaselineWithUpdate 基线（BVH）：每 tick 也做增量位置更新 + 查询，公平对比。
func BenchmarkBaselineWithUpdate(b *testing.B) {
	for _, l := range defaultLayouts() {
		w, idx, movers := buildScene(l)
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				for _, e := range movers {
					x, z := moverPos(w, e)
					idx.SetDynamic(e, x, z+0.5, 0.305, 0, 0, 0)
				}
				for _, e := range movers {
					x, z := moverPos(w, e)
					idx.Neighbors(x, z+0.5, l.Radius, e)
				}
			}
		})
	}
}

// BenchmarkGridCached 组合方案：增量网格 + 降频（每 4 tick 刷新邻居，其余走缓存）。
func BenchmarkGridCached(b *testing.B) {
	for _, l := range defaultLayouts() {
		w, _, movers := buildScene(l)
		g := collision.NewOrcaAOI(200, 200, 1)
		g.Reset()
		for _, e := range movers {
			x, z := moverPos(w, e)
			g.Add(e, x, z)
		}
		cacher := newNeighborCacher(4)
		var buf []collision.Neighbor
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// 网格每 tick 增量更新（便宜：多数不跨格）
				for _, e := range movers {
					x, z := moverPos(w, e)
					g.Add(e, x, z+0.5)
				}
				// 邻居按降频刷新
				refresh := cacher.shouldRefresh()
				if refresh {
					cacher.beginRefresh()
				}
				for _, e := range movers {
					if refresh {
						x, z := moverPos(w, e)
						ns := g.Neighbors(x, z+0.5, l.Radius, e,
							shapeOfEntity(w), buf)
						buf = ns[:0]
						cacher.put(e, ns)
						continue
					}
					_ = cacher.get(e)
				}
			}
		})
	}
}

// ---- 分离"实体总数"与"密度"两个变量 ----

// scaleLayout 在**固定密度**下改变实体总数：实体数越多，占地越大。
// 这样才能回答"时间上升是因为数量多，还是因为挤"。
func scaleLayout(total int, spacing int) benchLayout {
	return benchLayout{
		Name:    fmt.Sprintf("n%d_sp%d", total, spacing),
		Total:   total,
		Spacing: spacing,
		Radius:  10,
	}
}

// TestDensityVsCount 分离两个变量：
//
//	A 组：固定 128x128 占地（密度上升）—— 现在的布局
//	B 组：固定间距 4（密度不变，占地随数量扩大）
func TestDensityVsCount(t *testing.T) {
	t.Log("=== A 组：占地固定 ~128×128，密度上升 ===")
	for _, n := range []int{256, 1024, 4096} {
		// 让 side*spacing ≈ 128 -> spacing = 128/side
		side := int(math.Ceil(math.Sqrt(float64(n))))
		sp := 128 / side
		if sp < 1 {
			sp = 1
		}
		l := scaleLayout(n, sp)
		w, idx, movers := buildScene(l)
		counts := sumNeighborsBaseline(w, idx, movers, l.Radius)
		sum := 0
		for _, c := range counts {
			sum += c
		}
		t.Logf("  n=%5d 间距=%2d 占地≈%3d×%3d 平均邻居=%5.1f 总邻居对=%d",
			n, sp, side*sp, side*sp, float64(sum)/float64(n), sum)
	}

	t.Log("=== B 组：间距固定 4（密度不变），占地随数量扩大 ===")
	for _, n := range []int{256, 1024, 4096} {
		l := scaleLayout(n, 4)
		w, idx, movers := buildScene(l)
		counts := sumNeighborsBaseline(w, idx, movers, l.Radius)
		sum := 0
		for _, c := range counts {
			sum += c
		}
		side := int(math.Ceil(math.Sqrt(float64(n))))
		t.Logf("  n=%5d 间距= 4 占地≈%3d×%3d 平均邻居=%5.1f 总邻居对=%d",
			n, side*4, side*4, float64(sum)/float64(n), sum)
	}
}

// BenchmarkScaleFixedDensity 固定密度（间距 4）扩数量：验证"邻居数不随总量增长"。
func BenchmarkScaleFixedDensity(b *testing.B) {
	for _, n := range []int{256, 1024, 4096, 16384} {
		l := scaleLayout(n, 4)
		w, idx, movers := buildScene(l)
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sumNeighborsBaseline(w, idx, movers, l.Radius)
			}
		})
	}
}

// BenchmarkScaleFixedArea 固定占地（128×128）加密度：邻居数随密度线性增长。
func BenchmarkScaleFixedArea(b *testing.B) {
	for _, n := range []int{256, 1024, 4096} {
		side := int(math.Ceil(math.Sqrt(float64(n))))
		sp := 128 / side
		if sp < 1 {
			sp = 1
		}
		l := scaleLayout(n, sp)
		w, idx, movers := buildScene(l)
		b.Run(l.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sumNeighborsBaseline(w, idx, movers, l.Radius)
			}
		})
	}
}
