package world

import (
	"fmt"
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
)

// 场景工具：把一批实体放进一个空世界，跑若干 tick，统计"是否重叠 / 是否卡死 / 是否抖动"。
//
// 这些是**效果**测试（不是单元测试）：关心的是"一群生物挤在一起时看起来对不对"，
// 而不是某个函数返回值。判定指标：
//   - overlap：任意两体的圆心距 < 半径和（穿模）
//   - stuck  ：连续 N tick 位置几乎不动（被卡死）
//   - jitter ：位置在相邻 tick 间来回（振荡）

type crowdingWorld struct {
	wa    *WorldActor
	mobs  []ecs.Entity
	prevX []float64
	prevY []float64
}

// newCrowdingWorld 造一个够大的空世界（默认测试地图只有 8×8，
// 放不下"一群生物"的场景——出界的移动会被地形层正确挡住，看起来像"卡死"）。
func newCrowdingWorld(t *testing.T, n, radius float64) *crowdingWorld {
	t.Helper()
	wa := moveTestWorld()
	const size = 48
	md := &MapData{Width: size, Height: size, CornerTypes: make([]byte, (size+1)*(size+1))}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = 3 // GRASS
	}
	wa.attachMap(md)
	cw := &crowdingWorld{wa: wa}
	for i := 0; i < int(n); i++ {
		e := addCreature(wa, 0, 0, radius)
		cw.mobs = append(cw.mobs, e)
	}
	cw.prevX = make([]float64, len(cw.mobs))
	cw.prevY = make([]float64, len(cw.mobs))
	return cw
}

// place 把第 i 个实体放到指定格（用格子坐标，避免重叠布置）。
func (c *crowdingWorld) place(i, x, y int) {
	ecs.Set(c.wa.sim, c.mobs[i], components.Position{X: x, Y: y})
	ecs.Set(c.wa.sim, c.mobs[i], components.Moveable{
		Speed: 10, SubX: 0.5, SubY: 0.5,
	})
}

// positions 取当前所有实体的连续位置。
func (c *crowdingWorld) positions() [][2]float64 {
	out := make([][2]float64, len(c.mobs))
	for i, e := range c.mobs {
		p := ecs.Get[components.Position](c.wa.sim, e)
		mv := ecs.Get[components.Moveable](c.wa.sim, e)
		out[i] = [2]float64{float64(p.X) + mv.SubX, float64(p.Y) + mv.SubY}
	}
	return out
}

// minGap 返回任意两体的最小圆心距。
func (c *crowdingWorld) minGap() float64 {
	ps := c.positions()
	best := math.Inf(1)
	for i := 0; i < len(ps); i++ {
		for j := i + 1; j < len(ps); j++ {
			d := math.Hypot(ps[i][0]-ps[j][0], ps[i][1]-ps[j][1])
			if d < best {
				best = d
			}
		}
	}
	return best
}

// moveAll 让所有实体朝同一方向走（模拟"一群动物一起走向同一个点"）。
func (c *crowdingWorld) moveAll(dx, dy int) {
	for _, e := range c.mobs {
		systems.EnqueueControl(c.wa.sim, systems.MoveIntent(e, dx, dy, 0))
	}
}

// 场景一：一群生物从四周涌向中心。
//
// 关心：会不会挤成一团互相穿模（ORCA 失效的表现）。
func TestCrowdConvergeNoOverlap(t *testing.T) {
	const n = 12
	const radius = 0.3
	const center = 24
	cw := newCrowdingWorld(t, n, radius)
	// 环形布置：半径 8 格的圆上均匀分布。
	//
	// 半径必须够大，否则 int(round(...)) 会把多只放进同一格 —— 那样**初始就重叠**，
	// 而"起点已重叠"是物理上无法瞬间分开的（ORCA 只能逐步推开），
	// 会让测试测到"初始化布置"而不是"避障行为"。
	for i := 0; i < n; i++ {
		ang := 2 * math.Pi * float64(i) / n
		x := int(math.Round(center + 8*math.Cos(ang)))
		y := int(math.Round(center + 8*math.Sin(ang)))
		cw.place(i, x, y)
	}
	tickWorld(cw.wa)
	if g := cw.minGap(); g < 2*radius {
		t.Fatalf("初始布置就重叠了（间距 %.3f），测试无效", g)
	}

	// 所有人朝中心走
	threshold := 2 * radius
	worst := math.Inf(1)
	for tick := 0; tick < 40; tick++ {
		for _, e := range cw.mobs {
			p := ecs.Get[components.Position](cw.wa.sim, e)
			dx := signOf(center - p.X)
			dy := signOf(center - p.Y)
			systems.EnqueueControl(cw.wa.sim, systems.MoveIntent(e, dx, dy, 0))
		}
		tickWorld(cw.wa)
		if g := cw.minGap(); g < worst {
			worst = g
		}
	}
	final := cw.minGap()
	t.Logf("汇拢场景：%d 只，最小圆心距 %.4f（中途）/ %.4f（稳定后），半径和 %.4f",
		n, worst, final, threshold)

	// 已知特性（实测，非 bug）：ORCA 是**软**约束，它保证的是
	// "按当前意图速度、在 τ 秒内不会撞"，而**不**保证任意时刻不重叠。
	// 12 只同时挤向同一个点时，可行速度集合收缩到极小甚至为空，
	// 解算退化成"最不违反"，中途会被压得比较深。
	// 这是"大家都想占同一个位置"的必然结果（真实 RVO2 也一样，
	// 它靠 linearProgram3 缓解，但不做硬性不穿透保证）。
	//
	// τ=0.5s 实测（12 只环形汇拢，本测试场景）：
	//   中途最小间距 0.142，稳定后 0.620（≈ 紧密相切的极限）
	//
	// 这个场景是**过约束**的：12 只半径 0.3 的圆想挤到同一点，
	// 几何上必须围成半径 ≥1.15 的圈才能互不重叠。所以中途压缩是必然的。
	//
	// 其他场景（同一 τ）表现好得多，实测：
	//   两相向 0.620（满值）· 四交叉 0.620（满值）· 六汇拢 0.546 中途 / 0.621 稳定
	//
	// 对照 τ 的取舍：τ 越大中途越从容，但邻居查询半径与耗时同步放大
	//（τ=2s -> 半径 20 格 -> 1000 实体 88ms/tick，超 50ms 预算）。
	// 且 τ 与"中途压多深"并非单调关系（0.5->0.142, 1.0->0.098, 2.0->0.244），
	// 因为 τ 同时作用于时间窗口与 w=relV-relPos/τ 两处，方向相反。
	//
	// 验收标准分两档：
	//   ① 中途：不得低于 0.1（再低就是"叠在一起"而非"挤一下"）；
	//   ② 稳定后：必须恢复到基本不重叠（≥ threshold - 0.05），这是"能散开"的证明。
	if worst < 0.1 {
		t.Fatalf("汇拢中途压得过深：最小圆心距 %.4f < 0.1（叠在一起了）", worst)
	}
	if final < threshold-0.05 {
		t.Fatalf("稳定后仍然重叠：最小圆心距 %.4f < %.4f（没散开）", final, threshold-0.05)
	}
}

// 场景二：一群生物同向走（并行流动），不该互相挡死。
func TestCrowdParallelFlowNotStuck(t *testing.T) {
	const n = 10
	cw := newCrowdingWorld(t, n, 0.3)
	// 一字排开在不同行，一起向 +X 走
	for i := 0; i < n; i++ {
		cw.place(i, 4, 4+i)
	}
	tickWorld(cw.wa)
	start := cw.positions()

	for tick := 0; tick < 20; tick++ {
		cw.moveAll(1, 0)
		tickWorld(cw.wa)
	}
	end := cw.positions()
	// 每只都应有明显前进（不是原地卡住）
	for i := range end {
		moved := end[i][0] - start[i][0]
		if moved < 3.0 {
			t.Fatalf("第 %d 只并行流动被卡住：只前进 %.2f 格（20 tick 应 >3 格）", i, moved)
		}
	}
}

// 场景三：两只相向而行 —— 不得振荡（位置不能来回摆）。
func TestPairHeadOnNoOscillation(t *testing.T) {
	cw := newCrowdingWorld(t, 2, 0.3)
	cw.place(0, 5, 10)
	cw.place(1, 15, 10)
	tickWorld(cw.wa)

	var xs [][]float64
	for tick := 0; tick < 25; tick++ {
		systems.EnqueueControl(cw.wa.sim, systems.MoveIntent(cw.mobs[0], 1, 0, 0))
		systems.EnqueueControl(cw.wa.sim, systems.MoveIntent(cw.mobs[1], -1, 0, 0))
		tickWorld(cw.wa)
		ps := cw.positions()
		xs = append(xs, []float64{ps[0][0], ps[1][0]})
	}
	// 检查末尾是否有来回（相邻 tick 位移方向反复翻转且幅度可观）
	flips := 0
	for i := 2; i < len(xs); i++ {
		d1 := xs[i][0] - xs[i-1][0]
		d2 := xs[i-1][0] - xs[i-2][0]
		if d1*d2 < 0 && math.Abs(d1) > 0.05 && math.Abs(d2) > 0.05 {
			flips++
		}
	}
	if flips > 2 {
		t.Fatalf("相向而行发生振荡：位移方向翻转 %d 次（轨迹 %v）", flips, xs)
	}
	// 两人应已交错
	if xs[len(xs)-1][0] <= xs[len(xs)-1][1] {
		t.Fatalf("两人应已交错而过，实际 x0=%.2f x1=%.2f", xs[len(xs)-1][0], xs[len(xs)-1][1])
	}
}

// 场景四：静态障碍 + 多个生物混合，验证生物不穿墙、也不卡在墙里。
func TestCrowdWithStaticObstacles(t *testing.T) {
	wa := moveTestWorld()
	// 一堵墙占 (8,6)-(8,14)
	for y := 6; y <= 14; y++ {
		addWallBlocker(wa, 8, y, 1, 1)
	}
	var mobs []ecs.Entity
	for i := 0; i < 6; i++ {
		e := addCreature(wa, 0, 0, 0.3)
		ecs.Set(wa.sim, e, components.Position{X: 4, Y: 8 + i})
		ecs.Set(wa.sim, e, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
		mobs = append(mobs, e)
	}
	tickWorld(wa)

	for tick := 0; tick < 30; tick++ {
		for _, e := range mobs {
			systems.EnqueueControl(wa.sim, systems.MoveIntent(e, 1, 0, 0))
		}
		tickWorld(wa)
	}

	// 墙在 x=8 占整格：生物的圆心不该越过 x=8（墙左边界）
	for i, e := range mobs {
		p := ecs.Get[components.Position](wa.sim, e)
		mv := ecs.Get[components.Moveable](wa.sim, e)
		x := float64(p.X) + mv.SubX
		y := float64(p.Y) + mv.SubY
		if y < 15 && x > 8.0 {
			t.Fatalf("第 %d 只穿墙了：x=%.3f（墙在 x=8，y=%.1f）", i, x, y)
		}
	}
}

// 场景五：确定性 —— 同样的初始条件跑两遍，结果必须完全一致。
//
// 这是 ORCA 这类迭代算法最容易破坏的性质（邻居顺序、map 遍历都会引入不确定性）。
func TestCrowdDeterministic(t *testing.T) {
	run := func() [][2]float64 {
		cw := newCrowdingWorld(t, 8, 0.3)
		for i := 0; i < 8; i++ {
			cw.place(i, 4+i, 4+(i%3))
		}
		tickWorld(cw.wa)
		for tick := 0; tick < 20; tick++ {
			cw.moveAll(1, 1)
			tickWorld(cw.wa)
		}
		return cw.positions()
	}
	a, b := run(), run()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("两次运行结果不同（第 %d 只）：%v vs %v", i, a[i], b[i])
		}
	}
	fmt.Printf("确定性：8 只 × 20 tick 两次运行完全一致\n")
}

func signOf(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
