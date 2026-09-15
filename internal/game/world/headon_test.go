package world

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
)

// 相向而行的回归锁。
//
// 历史：静态碰撞与动态实体混在一次解算里（边算边写位置）时，两人会被同时硬挡、
// 下一 tick 又因"起点重叠推出"被弹回，实测间距在 1.0 与 0.22 之间以 20Hz 振荡
// （重叠 0.385 格）。三阶段 + ORCA 之后应当：互相绕开、保持不重叠、且不振荡。
func TestHeadOnAvoidance(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
	creature := addCreature(wa, 8, 4, 0.3)
	ecs.Set(wa.sim, creature, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
	tickWorld(wa)

	const threshold = systems.BodyRadius + 0.3 // 两者表面相切的最小圆心距
	minGap := math.Inf(1)
	var playerY, creatureY []float64

	for i := 0; i < 30; i++ {
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1, DY: 0}})
		systems.EnqueueControl(wa.sim, systems.MoveIntent(creature, -1, 0, 0))
		tickWorld(wa)

		pp := ecs.Get[components.Position](wa.sim, player)
		pmv := ecs.Get[components.Moveable](wa.sim, player)
		cp := ecs.Get[components.Position](wa.sim, creature)
		cmv := ecs.Get[components.Moveable](wa.sim, creature)
		px, py := float64(pp.X)+pmv.SubX, float64(pp.Y)+pmv.SubY
		cx, cy := float64(cp.X)+cmv.SubX, float64(cp.Y)+cmv.SubY

		if d := math.Hypot(px-cx, py-cy); d < minGap {
			minGap = d
		}
		playerY = append(playerY, py)
		creatureY = append(creatureY, cy)
	}

	// ① 不得重叠（这是"穿过动物"的直接回归）
	if minGap < threshold-1e-3 {
		t.Fatalf("相向而行发生重叠：最小间距 %.4f < 阈值 %.4f（重叠 %.4f）",
			minGap, threshold, threshold-minGap)
	}

	// ② 必须互相绕开：两人各自朝相反一侧偏移，而不是贴在一起
	playerShift := playerY[len(playerY)-1] - playerY[0]
	creatureShift := creatureY[len(creatureY)-1] - creatureY[0]
	if math.Abs(playerShift) < 0.1 {
		t.Fatalf("玩家应横向绕开，实际只偏移 %.4f", playerShift)
	}
	if playerShift*creatureShift >= 0 {
		t.Fatalf("两人应向相反两侧让：玩家 %.4f / 动物 %.4f", playerShift, creatureShift)
	}

	// ③ 不允许振荡：绕开之后应当稳定，而不是来回摆。
	//    取最后 6 tick 的横向位移，变化应远小于"每 tick 走 0.5 格"的量级。
	last := playerY[len(playerY)-6:]
	spread := 0.0
	for _, y := range last {
		if d := math.Abs(y - last[0]); d > spread {
			spread = d
		}
	}
	if spread > 0.05 {
		t.Fatalf("绕开后仍在横向振荡：最后 6 tick 摆幅 %.4f（y=%v）", spread, last)
	}
}

// 相向而行结束后双方都应继续前进（不能被"顶住"卡死）。
func TestHeadOnTheyPassThrough(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
	creature := addCreature(wa, 8, 4, 0.3)
	ecs.Set(wa.sim, creature, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
	tickWorld(wa)

	for i := 0; i < 30; i++ {
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1, DY: 0}})
		systems.EnqueueControl(wa.sim, systems.MoveIntent(creature, -1, 0, 0))
		tickWorld(wa)
	}
	pp := ecs.Get[components.Position](wa.sim, player)
	cp := ecs.Get[components.Position](wa.sim, creature)
	// 玩家从 x=2 出发、动物从 x=8 出发：走完 30 tick（各能走 15 格）后必然交错而过
	if pp.X <= cp.X {
		t.Fatalf("两人应已交错而过（玩家 %d 应在动物 %d 右侧），说明被顶住了", pp.X, cp.X)
	}
}
