package world

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
)

// moveTestWorld 8×8 全草地小世界（负方向移动回归用）。
func moveTestWorld() *WorldActor {
	wa := NewWorldActor(WorldConfig{})
	md := &MapData{Width: 8, Height: 8, CornerTypes: make([]byte, 9*9)}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = byte(3) // GRASS
	}
	wa.attachMap(md)
	return wa
}

// M7 连续速度：负方向（左/上）移动回归——sub 递减跨 0 借位提交，
// 四个方向对称。此前符号 bug 导致 sub 变负、永远不跨格（客户端预测与服务端打架 = 抽搐）。
func TestMoveNegativeDirections(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 4, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 20}) // 20 格/秒 × 50ms = 1 格/tick

	for _, dir := range []struct {
		dx, dy int
		wx, wy int
	}{
		{-1, 0, 3, 4}, // 左
		{0, -1, 3, 3}, // 上
		{1, 0, 4, 3},  // 右
		{0, 1, 4, 4},  // 下
	} {
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: dir.dx, DY: dir.dy}})
		tickWorld(wa)
		p := ecs.Get[components.Position](wa.sim, player)
		if p.X != dir.wx || p.Y != dir.wy {
			t.Fatalf("方向(%d,%d) 应到 (%d,%d)，实际 (%d,%d)", dir.dx, dir.dy, dir.wx, dir.wy, p.X, p.Y)
		}
		mv := ecs.Get[components.Moveable](wa.sim, player)
		if mv.SubX < 0 || mv.SubX >= 1 || mv.SubY < 0 || mv.SubY >= 1 {
			t.Fatalf("sub 应保持在 [0,1)，实际 sub=(%v,%v)", mv.SubX, mv.SubY)
		}
	}
}

// 负方向贴墙：向左撞墙应停在墙面前 body 半径处（sub≈BodyRadius），不再穿墙/抖动。
// 占位不再挡格以后，墙是"占格盒形状"：角色能贴到墙面外 0.2 格（过去只能停在格边界）。
func TestMoveNegativeWallStop(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 4, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 20})

	wall := wa.sim.CreateEntity()
	ecs.Add(wa.sim, wall, components.Building{Kind: components.BuildingWall, Width: 1, Height: 1})
	if !PlaceBuilding(wa.sim, wall, 2, 4) {
		t.Fatal("墙放置失败")
	}
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: -1, DY: 0}})
	for i := 0; i < 4; i++ {
		tickWorld(wa)
	}
	p := ecs.Get[components.Position](wa.sim, player)
	mv := ecs.Get[components.Moveable](wa.sim, player)
	if p.X != 3 {
		t.Fatalf("向左撞墙应停在 x=3，实际 %d", p.X)
	}
	// 墙占格 (2,4)：墙面在 x=3，角色半径 0.2 → 渲染位置应贴在 3.2
	wantX := 3 + systems.BodyRadius
	if math.Abs(float64(p.X)+mv.SubX-wantX) > 3e-3 {
		t.Fatalf("应停在墙面外 body 半径处 x≈%.2f，实际 %.4f", wantX, float64(p.X)+mv.SubX)
	}
	// 再多推几 tick 不应震荡/穿墙
	for i := 0; i < 4; i++ {
		tickWorld(wa)
	}
	if got := float64(p.X) + mv.SubX; math.Abs(got-wantX) > 3e-3 {
		t.Fatalf("持续顶墙应保持不动，实际 %.4f", got)
	}
}
