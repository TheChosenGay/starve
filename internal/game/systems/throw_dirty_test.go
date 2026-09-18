package systems

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 回归：**飞行中的投掷物必须每 tick 标脏 Thrown 与 Position**。
//
// 真实 bug：飞行位置只在"整格坐标变化"时才标脏 Position。飞行约 0.29 格/tick
// （8 格飞 28 tick）⇒ 客户端 2.5~3.5 tick 才拿到一个样本、每次跳**整 1 格**
// ⇒ 插值器外推 1 tick 就到顶、只能冻结等下一格 ⇒ **炸弹一格一顿**。
//
// 这与 Moveable 的同类 bug（见 move_dirty_test.go）是**同一个契约**：
// 移动相关组件"只要动了就标脏"，绝不能只在跨格/整格变化时标脏。
func TestFlyingProjectileMarkedDirtyEveryTick(t *testing.T) {
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)

	e := w.CreateEntity()
	ecs.Add(w, e, components.Position{X: 10, Y: 10})
	ecs.Add(w, e, components.Thrown{
		FromX: 10, FromY: 10,
		ToX: 18, ToY: 10,
		FlightTicks: 14, Gravity: 0.02,
	})

	thrownID := ecs.ComponentIDOf[components.Thrown](w)
	posID := ecs.ComponentIDOf[components.Position](w)
	ts := &ThrowSystem{}

	const ticks = 14
	thrownDirty, posDirty := 0, 0
	for i := 0; i < ticks; i++ {
		w.DrainDirty()
		ts.Update(w, 50*time.Millisecond)
		for _, ids := range w.DrainDirty() {
			for _, id := range ids {
				if id == thrownID {
					thrownDirty++
				}
				if id == posID {
					posDirty++
				}
			}
		}
	}

	if thrownDirty != ticks {
		t.Fatalf("Thrown 必须每 tick 标脏（客户端按 elapsed 还原抛物线）：%d/%d", thrownDirty, ticks)
	}
	if posDirty != ticks {
		t.Fatalf("Position 必须每 tick 标脏，不能只在整格变化时（否则炸弹一格一顿）：%d/%d", posDirty, ticks)
	}
}

// 回归：**爆炸物落地即消耗**（实体被销毁），不能在地上留一颗可拾取的炸弹。
//
// 曾经的 bug：land() 只 Remove[Thrown]，而炸弹实体带 Lootable ⇒ 爆炸后地上残留
// 可拾取炸弹，捡起来还能再投再炸 = 无限炸弹。
//
// 同时确认**非爆炸物**（石头/木头等）落地后仍然留在世界上（那是"落地物品"，不是花掉）。
func TestExplosiveProjectileIsConsumedOnLanding(t *testing.T) {
	newWorld := func(explosive bool) (*ecs.World, ecs.Entity) {
		w := ecs.NewWorld()
		components.RegisterCodecs(w, false)
		e := w.CreateEntity()
		ecs.Add(w, e, components.Position{X: 10, Y: 10})
		ecs.Add(w, e, components.Lootable{})
		ecs.Add(w, e, components.Thrown{
			FromX: 10, FromY: 10, ToX: 12, ToY: 10,
			FlightTicks: 4, Gravity: 0.02,
		})
		if explosive {
			ecs.Add(w, e, components.Explosive{Radius: 3, Damage: 20})
		}
		return w, e
	}

	ts := &ThrowSystem{}

	// ① 爆炸物：飞完就销毁
	w, bomb := newWorld(true)
	for i := 0; i < 5; i++ {
		ts.Update(w, 50*time.Millisecond)
	}
	if w.IsAlive(bomb) {
		t.Fatalf("爆炸物落地后必须被消耗（销毁实体），否则地上会残留可拾取炸弹 = 无限炸弹")
	}

	// ② 非爆炸物：飞完只摘掉 Thrown，物品留在原地可被拾取
	w2, rock := newWorld(false)
	for i := 0; i < 5; i++ {
		ts.Update(w2, 50*time.Millisecond)
	}
	if !w2.IsAlive(rock) {
		t.Fatalf("非爆炸物落地应留在世界上（它只是「落地物品」）")
	}
	if ecs.Has[components.Thrown](w2, rock) {
		t.Fatalf("落地后应摘掉 Thrown")
	}
}
