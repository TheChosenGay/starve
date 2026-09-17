package systems

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
	game "starve/pkg/proto/game"
)

// 贴墙不动时 Moveable.VelX/VelY 必须是 0。
//
// 背景：提交阶段先把求解器速度写进组件（move_system.go），**然后**才把它交给
// 格子层 ApplyDisplacement。格子层可能整段拒收——stepAxis 在目标格不可走时
// 把 sub 钳在 0.999，这一 tick 零位移，但 VelX/VelY 仍是满速。
//
// 组件与 proto 都写明 vel 是"实际速度 / 实际写入 sub 的速度，静止时为 (0,0)"，
// 客户端 PositionSmoother 正是靠"vel==(0,0) 表示服务端确认停止"来关闭外推。
// 因此贴墙仍报满速 = 客户端朝不可走方向外推 → 残差触发混合拉回 = 橡皮筋。
func TestMoveSystem_PinnedAtWallReportsZeroVelocity(t *testing.T) {
	sim := ecs.NewWorld()
	// 16x16 平地，在 (6,5) 放水（硬墙），实体从 (5,5) 朝 +X 顶上去。
	md := &worldmap.MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)}
	sim.AddResource(md)
	const wallX, wallY = 6, 5
	md.CornerTypes[wallY*17+wallX] = byte(game.TerrainType_TERRAIN_TYPE_WATER)
	sim.AddResource(collision.NewIndex())

	// 起点子格 0.9：先正常走，再被墙挡住——覆盖"撞墙那一刻"和"钉住之后"。
	e := sim.CreateEntity()
	ecs.Add(sim, e, components.Position{X: 5, Y: 5})
	ecs.Add(sim, e, components.Moveable{
		Speed: 10, DirX: 1, DirY: 0,
		SubX: 0.9, SubY: 0,
	})

	sys := &MoveSystem{}
	dt := 50 * time.Millisecond // 20Hz，与服务器一致

	// 20 tick 足够走完 0.1 格并撞墙钉住。
	var lastX, lastSub float64
	for i := 0; i < 20; i++ {
		sys.Update(sim, dt)
		p := ecs.Get[components.Position](sim, e)
		mv := ecs.Get[components.Moveable](sim, e)
		lastX, lastSub = float64(p.X), mv.SubX
	}

	p := ecs.Get[components.Position](sim, e)
	mv := ecs.Get[components.Moveable](sim, e)

	// 前提：确实停在墙前（锚点没跨过去）。
	if p.X != 5 {
		t.Fatalf("实体跨过了墙：p.X=%d，期望 5（水中不可走）", p.X)
	}
	if mv.SubX != 0.999 {
		t.Fatalf("期望被钳在边界 0.999，实际 sub=%.4f", mv.SubX)
	}

	// 记录"静止"的这几 tick 是否有位移，再断言速度。
	beforeX, beforeSub := float64(p.X), mv.SubX
	for i := 0; i < 5; i++ {
		sys.Update(sim, dt)
	}
	p2 := ecs.Get[components.Position](sim, e)
	mv2 := ecs.Get[components.Moveable](sim, e)
	moved := (float64(p2.X) + mv2.SubX) - (beforeX + beforeSub)

	if moved != 0 {
		t.Fatalf("前提不成立：贴墙期间仍位移了 %.4f 格", moved)
	}

	// 核心断言：零位移 → 速度必须为 0。
	if mv2.VelX != 0 || mv2.VelY != 0 {
		t.Fatalf("贴墙零位移，但 VelX/VelY = (%.3f, %.3f)，期望 (0, 0)——"+
			"客户端会据此朝墙外推，产生橡皮筋", mv2.VelX, mv2.VelY)
	}
	_ = lastX
	_ = lastSub
}

// 反向护栏：畅通无阻时速度必须仍然是满速。
// 防止"被挡就清零"式的过度修正把正常移动也一起打成 0（客户端会停止外推→顿卡）。
func TestMoveSystem_FreeMoveKeepsFullVelocity(t *testing.T) {
	sim := ecs.NewWorld()
	md := &worldmap.MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)}
	sim.AddResource(md)
	sim.AddResource(collision.NewIndex())

	e := sim.CreateEntity()
	ecs.Add(sim, e, components.Position{X: 5, Y: 5})
	ecs.Add(sim, e, components.Moveable{Speed: 10, DirX: 1, DirY: 0})

	sys := &MoveSystem{}
	dt := 50 * time.Millisecond
	// 先跑几 tick 让状态稳定，再看速度。
	for i := 0; i < 5; i++ {
		sys.Update(sim, dt)
	}
	mv := ecs.Get[components.Moveable](sim, e)
	if mv.VelX < 9.99 || mv.VelX > 10.01 || mv.VelY != 0 {
		t.Fatalf("畅通时 VelX/VelY = (%.4f, %.4f)，期望约 (10, 0)", mv.VelX, mv.VelY)
	}
	// 且确实在移动（跨格 + 子格位移都在发生）。
	if float64(ecs.Get[components.Position](sim, e).X)+mv.SubX <= 5 {
		t.Fatal("畅通时应当有实际位移")
	}
}
