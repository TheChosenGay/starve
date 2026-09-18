package systems

import (
	"math"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// 完全共线正面对撞：对称打破必须让双方**各让一侧**，而且谁也顶不死。
//
// 这是真实教训的回归（P1.4 §5.1）：
//   - 服务端生产曾经用 `NewORCASolver`（没有分侧），而客户端预测开着对称打破
//     ⇒ 退化点上两端选到相反一侧，差半个身位（不是 1e-5 的浮点噪声）——
//     表现为明显的位置校正/橡皮筋；
//   - 服务端自己也会"同侧让" ⇒ 两个单位互相顶住。
//
// 这里刻意走**生产同款** solver（NewDefaultMoveSolver），并且覆盖奇偶两种 id：
// 分侧由 id 奇偶决定，只验一种等于只验一半（改坏一半看不出来）。

// collinearWorld 造一对**完全共线**正面对撞的单位（零横向偏移），返回 (世界, A, B)。
//
// aEven 控制 A 的 id 奇偶：实体 id 从 1 开始，需要偶数就先占一个无组件的探针实体
// （与语料生成器同一手法；探针不带组件 ⇒ 对求解不可见）。
func collinearWorld(aEven bool) (*ecs.World, ecs.Entity, ecs.Entity) {
	w := ecs.NewWorld()
	// 小地图：与语料一致（提供可走性/高度查询），这里全是平地。
	md := &worldmap.MapData{
		Width: 24, Height: 24,
		CornerTypes:   make([]byte, 25*25),
		CornerHeights: make([]byte, 25*25),
	}
	w.AddResource(md)
	// 碰撞索引：SyncDynamicBodies 把"带 Moveable + Collide"的实体写进动态层，
	// ORCA 邻居就是从这一层收集的 —— 少了它两边都收不到邻居、退化场景直接对穿，
	// 测试会误以为"对称打破没生效"（实测踩过）。
	w.AddResource(collision.NewIndex())

	mk := func(x, dirX int) ecs.Entity {
		e := w.CreateEntity()
		ecs.Add(w, e, components.Position{X: x, Y: 12})
		ecs.Add(w, e, components.Moveable{
			Speed: 8, EffectiveSpeed: 8, DirX: dirX, DirY: 0,
		})
		ecs.Add(w, e, components.Collide{
			Shape: components.CollideShapeCapsule, Radius: 0.3,
		})
		return e
	}

	if aEven {
		w.CreateEntity() // 探针：把 A 推到偶数 id
	}
	a := mk(12, 1)
	b := mk(14, -1)
	return w, a, b
}

// runCollinear 跑 n tick 生产同款移动，返回两实体末尾的连续位置。
func runCollinear(w *ecs.World, a, b ecs.Entity, n int) (ax, ay, bx, by float64) {
	ms := &MoveSystem{Solver: NewDefaultMoveSolver()}
	for i := 0; i < n; i++ {
		ms.Update(w, 50*time.Millisecond)
	}
	pa, pb := *ecs.Get[components.Position](w, a), *ecs.Get[components.Position](w, b)
	ma, mb := ecs.Get[components.Moveable](w, a), ecs.Get[components.Moveable](w, b)
	return float64(pa.X) + ma.SubX, float64(pa.Y) + ma.SubY,
		float64(pb.X) + mb.SubX, float64(pb.Y) + mb.SubY
}

func TestCollinearHeadOnEachGivesWayAndNobodyStalls(t *testing.T) {
	for _, aEven := range []bool{false, true} {
		name := "A_odd_id"
		if aEven {
			name = "A_even_id"
		}
		t.Run(name, func(t *testing.T) {
			w, a, b := collinearWorld(aEven)
			ax, ay, bx, by := runCollinear(w, a, b, 40)

			// ① 各让一侧：横向偏移必须**反号**（同号 = 往同一侧让 = 还是撞/顶死）
			da, db := ay-12, by-12
			if da == 0 && db == 0 {
				t.Fatal("双方都没有横向避让：对称打破没生效（退化点上两人会一直顶住）")
			}
			if da*db >= 0 {
				t.Fatalf("双方应往相反两侧让，实际 A 横向 %+.4f、B 横向 %+.4f —— "+
					"同侧让正是「服务端关掉对称打破」的旧行为", da, db)
			}
			// ② 没顶死：40 tick（2 秒）内必须互换前后位置（A 从左边跑到右边）
			if !(ax > bx) {
				t.Fatalf("两人没有互相穿过（A.x=%.3f 应 > B.x=%.3f）—— 顶死在接触面上", ax, bx)
			}
			// ③ 两人都真的在走（不是原地抖）
			if math.Abs(ax-12) < 1 || math.Abs(bx-14) < 1 {
				t.Fatalf("位移过小，疑似卡住：A.x=%.3f B.x=%.3f", ax, bx)
			}
		})
	}
}

// 同一对实体、同样输入，两次运行结果必须逐位相同。
//
// 退化点的分侧若依赖浮点噪声，这里会随机红 —— 这条守住"分侧是确定性的"。
func TestCollinearHeadOnIsDeterministic(t *testing.T) {
	w1, a1, b1 := collinearWorld(false)
	ax1, ay1, bx1, by1 := runCollinear(w1, a1, b1, 25)
	w2, a2, b2 := collinearWorld(false)
	ax2, ay2, bx2, by2 := runCollinear(w2, a2, b2, 25)

	if ax1 != ax2 || ay1 != ay2 || bx1 != bx2 || by1 != by2 {
		t.Fatalf("两次运行结果不同（分侧依赖了噪声）：\n  A (%.9f,%.9f) vs (%.9f,%.9f)\n  B (%.9f,%.9f) vs (%.9f,%.9f)",
			ax1, ay1, ax2, ay2, bx1, by1, bx2, by2)
	}
}
