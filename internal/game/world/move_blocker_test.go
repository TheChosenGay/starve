package world

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	"starve/internal/game/worldmap"
	game "starve/pkg/proto/game"
)

// addTreeBlocker 摆一棵"占不满一格"的树：格心圆占位物（形状 + 占位代价，不挡格）。
func addTreeBlocker(wa *WorldActor, x, y int, radius float64) ecs.Entity {
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Block{Radius: radius})
	return e
}

// addWallBlocker 摆一堵占满整格的墙：占格盒占位物。
func addWallBlocker(wa *WorldActor, x, y, w, h int) ecs.Entity {
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Block{Width: w, Height: h})
	return e
}

// walkTicks 按方向移动若干 tick，返回渲染位置（锚点 + 子格偏移）。
func walkTicks(wa *WorldActor, player ecs.Entity, dx, dy, ticks int) (float64, float64) {
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: dx, DY: dy}})
	for i := 0; i < ticks; i++ {
		tickWorld(wa)
	}
	p := ecs.Get[components.Position](wa.sim, player)
	mv := ecs.Get[components.Moveable](wa.sim, player)
	return float64(p.X) + mv.SubX, float64(p.Y) + mv.SubY
}

// setWater 把一格改成水（硬墙）：占位物不是硬墙，只有地形（水/悬崖）不可走。
func setWater(md *MapData, x, y int) {
	md.CornerTypes[y*(md.Width+1)+x] = byte(game.TerrainType_TERRAIN_TYPE_WATER)
}

// 贴树干：占位不等于不可走，角色能走进树所在格、停在树干边缘，而不是离树整整一格外停下。
func TestMoveStopsAtTrunkEdge(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 4, Y: 4})
	// 站在第 4 行格心线（y=4.5），正对树心 (5.5,4.5) 推进
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
	addTreeBlocker(wa, 5, 4, 0.18)

	x, y := walkTicks(wa, player, 1, 0, 10)
	p := ecs.Get[components.Position](wa.sim, player)
	if p.X != 5 {
		t.Fatalf("应走进树所在格（锚点 5），实际锚点 %d / x=%.4f", p.X, x)
	}
	wantX := 5.5 - 0.18 - systems.BodyRadius // 树干半径 + 身体半径
	if math.Abs(x-wantX) > 3e-3 {
		t.Fatalf("应停在树干边缘 x≈%.4f，实际 %.4f（整格阻挡会停在 5.0）", wantX, x)
	}
	if math.Abs(y-4.5) > 1e-9 {
		t.Fatalf("正面推进不应产生横向偏移, y=%.6f", y)
	}
}

// 擦过树干：剩余位移投影到接触切面 → 角色贴树干滑过去，不会原地卡住。
func TestMoveSlidesAroundTrunk(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 4, Y: 4})
	// y = 4.4：与树心 4.5 偏 0.1（小于 半径和 0.38）→ 会擦上树干
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.4})
	addTreeBlocker(wa, 5, 4, 0.18)

	x, y := walkTicks(wa, player, 1, 0, 12)
	if x <= 5.6 {
		t.Fatalf("应绕过树干继续前进（x > 5.6），实际 x=%.4f y=%.4f", x, y)
	}
	if y >= 4.4 {
		t.Fatalf("应被树干推向 -Y 侧滑，实际 y=%.4f", y)
	}
	if d := math.Hypot(x-5.5, y-4.5); d < 0.18+systems.BodyRadius-1e-6 {
		t.Fatalf("滑动后不得嵌进树干：圆心距 %.4f < 半径和 %.4f", d, 0.18+systems.BodyRadius)
	}
}

// 贴墙走：建筑的占格盒也是形状碰撞——斜着撞墙会沿墙面滑，且能贴到身体半径距离。
func TestMoveSlidesAlongWallFace(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
	addWallBlocker(wa, 4, 4, 1, 3) // 墙占格 (4,4)-(4,6)：x∈[4,5], y∈[4,7]

	// 朝 +X 直走：应停在墙面外 body 半径处（x = 4 - 0.2），而不是格边界 4.0 附近
	x, _ := walkTicks(wa, player, 1, 0, 12)
	if math.Abs(x-(4-systems.BodyRadius)) > 3e-3 {
		t.Fatalf("应停在墙面前 x≈%.3f，实际 %.4f", 4-systems.BodyRadius, x)
	}

	// 斜着推：沿墙面滑走（y 增加），x 保持贴墙（还没滑过墙的上沿）
	x, y := walkTicks(wa, player, 1, 1, 6)
	if math.Abs(x-(4-systems.BodyRadius)) > 3e-3 {
		t.Fatalf("滑动时应保持贴墙 x≈%.3f，实际 %.4f", 4-systems.BodyRadius, x)
	}
	if y <= 5.5 {
		t.Fatalf("应沿墙面往上滑（y > 5.5），实际 y=%.4f", y)
	}
}

// 树被砍倒后占位注销：同一路线可以直接穿过原来那格。
func TestMoveThroughChoppedTreeTile(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 4, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
	tree := addTreeBlocker(wa, 5, 4, 0.18)

	x, _ := walkTicks(wa, player, 1, 0, 10)
	if x > 5.2 {
		t.Fatalf("砍伐前应被树干挡住, x=%.4f", x)
	}
	ecs.Remove[components.Block](wa.sim, tree) // 砍倒 → 解除占位与形状
	x, _ = walkTicks(wa, player, 1, 0, 10)
	if x < 6.4 {
		t.Fatalf("砍倒后应能穿过原树位, x=%.4f", x)
	}
	md := ecs.Resource[MapData](wa.sim)
	if md.IsOccupied(5, 4) {
		t.Fatal("砍倒后占位代价应清除")
	}
}

// 卡在树干里（读档/传送/树长出来）时能被推出去，而不是永久卡死。
func TestMovePushesOutOfTrunk(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 5, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5}) // 正好站在树心
	addTreeBlocker(wa, 5, 4, 0.18)

	x, y := walkTicks(wa, player, 1, 0, 1)
	if d := math.Hypot(x-5.5, y-4.5); d < 0.18+systems.BodyRadius {
		t.Fatalf("一 tick 内应被推出树干，圆心距 %.4f < %.4f", d, 0.18+systems.BodyRadius)
	}
}

// 占位格仍然可走：水/悬崖才不可走，占位物只影响寻路代价。
func TestOccupiedTileIsWalkable(t *testing.T) {
	wa := moveTestWorld()
	md := ecs.Resource[MapData](wa.sim)
	addTreeBlocker(wa, 4, 4, 0.18)
	if !md.Walkable(4, 4) {
		t.Fatal("占位格地形应可走")
	}
	if !md.IsOccupied(4, 4) {
		t.Fatal("占位格应登记占位")
	}
	setWater(md, 5, 4)
	if md.Walkable(5, 4) {
		t.Fatal("水格不可走")
	}
}

// 占位代价进寻路：A* 宁愿多走两步绕过树干，也不从树干中间穿过去。
func TestFindPathDetoursAroundTrunk(t *testing.T) {
	wa := moveTestWorld()
	md := ecs.Resource[MapData](wa.sim)
	addTreeBlocker(wa, 4, 4, 0.18)

	path := worldmap.FindPath(md, 2, 4, 6, 4)
	if len(path) != 6 {
		t.Fatalf("绕行一格应为 6 步，实际 %d: %v", len(path), path)
	}
	if crossesTile(2, 4, path, 4, 4) {
		t.Fatalf("应绕开树干，实际穿过 (4,4): %v", path)
	}
}

// 占格盒（建筑/墙）代价极高：能绕就绕；但"不是墙"——唯一的通路被墙占住时仍给得出路径。
func TestFindPathAroundAndThroughWall(t *testing.T) {
	t.Run("prefer_detour", func(t *testing.T) {
		wa := moveTestWorld()
		md := ecs.Resource[MapData](wa.sim)
		addWallBlocker(wa, 4, 4, 1, 1)
		path := worldmap.FindPath(md, 2, 4, 6, 4)
		if crossesTile(2, 4, path, 4, 4) {
			t.Fatalf("墙占格代价极高，应绕开，实际穿过 (4,4): %v", path)
		}
		if len(path) != 6 {
			t.Fatalf("绕一堵墙应为 6 步，实际 %d: %v", len(path), path)
		}
	})

	t.Run("through_when_only_route", func(t *testing.T) {
		wa := moveTestWorld()
		md := ecs.Resource[MapData](wa.sim)
		// x=4 整列是水，只留 (4,4) 一个缺口，缺口上是墙 → 只能"穿"（代价高但有限）
		for y := 0; y < md.Height; y++ {
			if y != 4 {
				setWater(md, 4, y)
			}
		}
		addWallBlocker(wa, 4, 4, 1, 1)
		path := worldmap.FindPath(md, 2, 4, 6, 4)
		if !crossesTile(2, 4, path, 4, 4) {
			t.Fatalf("唯一通路时应穿过（占位不是硬墙），实际 %v", path)
		}
	})
}

// crossesTile 按路径步进，判断是否经过格 (x,y)。
func crossesTile(fromX, fromY int, path []components.MoveDir, x, y int) bool {
	cx, cy := fromX, fromY
	for _, d := range path {
		cx, cy = cx+d.DX, cy+d.DY
		if cx == x && cy == y {
			return true
		}
	}
	return false
}
