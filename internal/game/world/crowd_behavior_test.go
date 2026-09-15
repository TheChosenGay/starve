package world

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
)

// 观察"一群物体互相挤开"的**过程**。
//
// 这些是诊断型测试（-short 时跳过），用实测数字回答三个问题：
//   - 是"平稳散开"还是"抖来抖去"？
//   - 会不会有人被挤穿、或者卡死不动？
//   - 挤完之后是否稳定（不再互相推）？
//
// 2026-09-15 实测（τ=0.5s、邻居半径 10 格）：
//
//	散开场景（16 只挤在 4×4，朝四周走 30 tick）：
//	  最小间距稳定在 0.984（阈值 0.600，无挤压）；平均离中心 4.0 → 16.5
//	  说明"真的在散开"，且全程不重叠。
//
//	汇拢场景（10 只从半径 6 的环上挤向同一点，40 tick）：
//	  中途最小间距 0.314 → 最终 0.481（仍低于阈值 0.600）
//	  但**最后 32 tick 完全静止在 0.481，摆幅 0.000** —— 稳定，不抖
//
//	→ 结论：ORCA 是**软**约束，被挤到"物理上无法都不重叠"时它会接受
//	  一定重叠（0.481 = 半径和的 80%），但**不会振荡**。要硬保证不重叠
//	  需要额外的推挤解算（Pushable 已就位，尚未实现）。
const bodyRadius = 0.3

// bigWorld 造一个 size×size 的空草地世界。
// 注意：moveTestWorld 只有 8×8，实体放到 10 以外就出界了（移动被正确地挡住），
// 诊断多物体场景必须用够大的地图。
func bigWorld(size int) *WorldActor {
	wa := moveTestWorld()
	md := &MapData{Width: size, Height: size, CornerTypes: make([]byte, (size+1)*(size+1))}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = 3 // GRASS
	}
	wa.attachMap(md)
	return wa
}

// crowdSnapshot 取当前所有实体的位置。
func crowdSnapshot(wa *WorldActor, ents []ecs.Entity) [][2]float64 {
	out := make([][2]float64, len(ents))
	for i, e := range ents {
		p := ecs.Get[components.Position](wa.sim, e)
		mv := ecs.Get[components.Moveable](wa.sim, e)
		out[i] = [2]float64{float64(p.X) + mv.SubX, float64(p.Y) + mv.SubY}
	}
	return out
}

// minPairGap 返回任意两体的最小圆心距，以及是哪两个。
func minPairGap(ps [][2]float64) (float64, int, int) {
	best := math.Inf(1)
	bi, bj := -1, -1
	for i := 0; i < len(ps); i++ {
		for j := i + 1; j < len(ps); j++ {
			d := math.Hypot(ps[i][0]-ps[j][0], ps[i][1]-ps[j][1])
			if d < best {
				best, bi, bj = d, i, j
			}
		}
	}
	return best, bi, bj
}

// renderCrowd 把一批实体画成 ASCII 俯视图（诊断用）。
func renderCrowd(ps [][2]float64, size int) string {
	grid := make([][]byte, size)
	for i := range grid {
		grid[i] = make([]byte, size)
		for j := range grid[i] {
			grid[i][j] = '.'
		}
	}
	for _, p := range ps {
		x, y := int(p[0]), int(p[1])
		if x >= 0 && x < size && y >= 0 && y < size {
			grid[y][x] = '#'
		}
	}
	var b strings.Builder
	for _, row := range grid {
		b.Write(row)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestCrowdPushApartVisual 让一群实体从一个小区域出发四散，打印每几 tick 的形态。
func TestCrowdPushApartVisual(t *testing.T) {
	if testing.Short() {
		t.Skip("诊断型测试，-short 时跳过")
	}
	const n = 16
	const size = 48
	wa := bigWorld(size)

	// 16 只挤在 4×4 的方格上（间距 1 格，会重叠：半径 0.3 -> 直径 0.6 < 1，其实不重叠）
	ents := make([]ecs.Entity, 0, n)
	for i := 0; i < n; i++ {
		e := addCreature(wa, 0, 0, bodyRadius)
		ecs.Set(wa.sim, e, components.Position{X: 20 + i%4, Y: 20 + i/4})
		ecs.Set(wa.sim, e, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
		ents = append(ents, e)
	}
	tickWorld(wa)
	start := crowdSnapshot(wa, ents)
	g0, _, _ := minPairGap(start)
	t.Logf("初始：%d 只挤在 4×4 区域内，最小间距 %.3f（半径和 %.3f）", n, g0, 2*bodyRadius)
	t.Logf("判读要点：最小间距衡量\"有没有被挤穿\"，平均离中心衡量\"有没有真的散开\"")
	t.Logf("初始形态：\n%s", renderCrowd(start, size))

	// 让它们朝四周散开（每个朝远离中心的方向）
	center := 21.5
	_ = center
	threshold := 2 * bodyRadius
	for tick := 0; tick < 30; tick++ {
		for _, e := range ents {
			p := ecs.Get[components.Position](wa.sim, e)
			mv := ecs.Get[components.Moveable](wa.sim, e)
			x := float64(p.X) + mv.SubX
			y := float64(p.Y) + mv.SubY
			// 朝远离中心的方向走；**中心附近也用确定的非零方向**，
			// 否则"dx=dy=0"的实体会原地不动，看起来像"挤不开"。
			dx := signOf(int(x*2) - int(center*2))
			dy := signOf(int(y*2) - int(center*2))
			if dx == 0 && dy == 0 {
				// 正好在中心：按实体 id 决定往哪个象限散（保证确定性且都动起来）
				if int(e)%2 == 0 {
					dx = 1
				} else {
					dx = -1
				}
				if (int(e)/2)%2 == 0 {
					dy = 1
				} else {
					dy = -1
				}
			}
			systems.EnqueueControl(wa.sim, systems.MoveIntent(e, dx, dy, 0))
		}
		tickWorld(wa)
		if tick%5 == 4 {
			ps := crowdSnapshot(wa, ents)
			g, _, _ := minPairGap(ps)
			// 顺便打印"整体扩散程度"：到中心的平均距离（判断是否真的在散开）
			sum := 0.0
			for _, q := range ps {
				sum += math.Hypot(q[0]-center, q[1]-center)
			}
			t.Logf("tick %2d：最小间距 %.3f  平均离中心 %.2f", tick+1, g, sum/float64(len(ps)))
		}
	}
	final := crowdSnapshot(wa, ents)
	gf, gi, gj := minPairGap(final)
	t.Logf("最终：最小间距 %.3f（阈值 %.3f）实体 %d 与 %d", gf, threshold, gi, gj)
	if gf < threshold-0.05 {
		t.Logf("⚠️  仍有重叠：%.3f < %.3f（重叠 %.3f）", gf, threshold, threshold-gf)
	}
	t.Logf("最终形态：\n%s", renderCrowd(final, size))
}

// TestCrowdConvergeStability 一群人挤向同一点，观察最终是否稳定（不再来回推）。
func TestCrowdConvergeStability(t *testing.T) {
	if testing.Short() {
		t.Skip("诊断型测试，-short 时跳过")
	}
	const n = 10
	const size = 48
	wa := bigWorld(size)

	// 环形布置（半径 6 格，初始不重叠）
	ents := make([]ecs.Entity, 0, n)
	for i := 0; i < n; i++ {
		ang := 2 * math.Pi * float64(i) / n
		x := int(math.Round(24 + 6*math.Cos(ang)))
		y := int(math.Round(24 + 6*math.Sin(ang)))
		e := addCreature(wa, 0, 0, bodyRadius)
		ecs.Set(wa.sim, e, components.Position{X: x, Y: y})
		ecs.Set(wa.sim, e, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
		ents = append(ents, e)
	}
	tickWorld(wa)

	g0, _, _ := minPairGap(crowdSnapshot(wa, ents))
	t.Logf("初始最小间距 %.3f（环形半径 6）", g0)

	threshold := 2 * bodyRadius
	worst := math.Inf(1)
	track := make([]float64, 0, 40)
	for tick := 0; tick < 40; tick++ {
		for _, e := range ents {
			p := ecs.Get[components.Position](wa.sim, e)
			systems.EnqueueControl(wa.sim, systems.MoveIntent(e, signOf(24-p.X), signOf(24-p.Y), 0))
		}
		tickWorld(wa)
		g, _, _ := minPairGap(crowdSnapshot(wa, ents))
		track = append(track, g)
		if g < worst {
			worst = g
		}
	}
	final, fi, fj := minPairGap(crowdSnapshot(wa, ents))
	t.Logf("中途最小间距 %.3f / 最终 %.3f（阈值 %.3f，实体 %d-%d）", worst, final, threshold, fi, fj)

	// 稳定性：最后 8 tick 的最小间距变化幅度
	last := track[len(track)-8:]
	lo, hi := last[0], last[0]
	for _, v := range last {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	t.Logf("最后 8 tick 间距范围 [%.3f, %.3f]，摆幅 %.3f", lo, hi, hi-lo)
	if hi-lo > 0.15 {
		t.Logf("⚠️  末尾仍在变动（摆幅 %.3f）——可能存在持续推搡", hi-lo)
	} else {
		t.Logf("✓ 末尾已稳定（摆幅 %.3f）", hi-lo)
	}
	t.Logf("轨迹（每 tick 最小间距）：%s", fmtFloats(track))
}

func fmtFloats(vs []float64) string {
	parts := make([]string, len(vs))
	for i, v := range vs {
		parts[i] = fmt.Sprintf("%.2f", v)
	}
	return strings.Join(parts, " ")
}

// TestCrowdThroughput 多物体场景的**整体吞吐**：n 个实体跑 m tick，统计实际耗时。
func TestCrowdThroughput(t *testing.T) {
	if testing.Short() {
		t.Skip("诊断型测试，-short 时跳过")
	}
	for _, n := range []int{100, 500, 1000} {
		const size = 200
		wa := bigWorld(size)

		side := int(math.Ceil(math.Sqrt(float64(n))))
		ents := make([]ecs.Entity, 0, n)
		for i := 0; i < n; i++ {
			e := addCreature(wa, 0, 0, bodyRadius)
			ecs.Set(wa.sim, e, components.Position{X: i%side*4 + 2, Y: i/side*4 + 2})
			ecs.Set(wa.sim, e, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
			ents = append(ents, e)
		}
		tickWorld(wa)

		const ticks = 20
		start := nowNanos()
		for tick := 0; tick < ticks; tick++ {
			for _, e := range ents {
				systems.EnqueueControl(wa.sim, systems.MoveIntent(e, 1, 0, 0))
			}
			tickWorld(wa)
		}
		elapsed := nowNanos() - start
		perTick := elapsed / ticks
		sort.Slice(ents, func(i, j int) bool { return ents[i] < ents[j] })
		g, _, _ := minPairGap(crowdSnapshot(wa, ents))
		t.Logf("n=%5d: %d tick 共 %.2f ms，每 tick %.2f ms（预算 50ms 的 %.1f%%），最小间距 %.3f",
			n, ticks, float64(elapsed)/1e6, float64(perTick)/1e6,
			100*float64(perTick)/1e6/50.0, g)
	}
}

// nowNanos 取当前时间（纳秒），用于吞吐测量。
func nowNanos() int64 { return time.Now().UnixNano() }
