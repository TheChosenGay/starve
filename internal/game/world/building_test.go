package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// newBuildingWorld 带可走网格的世界（5×5 全可走 + WalkGrid 资源）。
func newBuildingWorld(t *testing.T) *WorldActor {
	t.Helper()
	wa := NewWorldActor(WorldConfig{})
	md := &MapData{Width: 5, Height: 5, CornerTypes: make([]byte, 6*6)}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = byte(3) // GRASS
	}
	wa.sim.AddResource(md)
	if wg, ok := ecs.TryResource[worldmap.WalkGrid](wa.sim); ok {
		wg.Rebuild(md)
	}
	return wa
}

// 放置：补 Position + placed=true + 批量 SetBlocked；重复放置/不可走拒绝。
func TestBuildingPlace(t *testing.T) {
	wa := newBuildingWorld(t)
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Building{Kind: components.BuildingCampfire, Width: 1, Height: 1})

	if !PlaceBuilding(wa.sim, e, 2, 2) {
		t.Fatal("放置应成功")
	}
	b := ecs.Get[components.Building](wa.sim, e)
	if !b.Placed {
		t.Fatal("放置后 placed=true")
	}
	p := ecs.Get[components.Position](wa.sim, e)
	if p.X != 2 || p.Y != 2 {
		t.Fatalf("位置 = (%d,%d), want (2,2)", p.X, p.Y)
	}
	wg := ecs.Resource[worldmap.WalkGrid](wa.sim)
	if wg.Walkable(2, 2) {
		t.Fatal("占格应被阻挡")
	}
	if !ecs.Has[components.HeatSource](wa.sim, e) {
		t.Fatal("火堆放置后应挂 HeatSource")
	}
	if PlaceBuilding(wa.sim, e, 3, 3) {
		t.Fatal("已放置建筑不应重复放置")
	}
	e2 := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e2, components.Building{Kind: components.BuildingWall, Width: 1, Height: 1})
	if PlaceBuilding(wa.sim, e2, 2, 2) {
		t.Fatal("被占格不应再放置")
	}
}

// 二维尺寸：2×1 建筑占两格，拆除全部恢复。
func TestBuildingSize2D(t *testing.T) {
	wa := newBuildingWorld(t)
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Building{Kind: components.BuildingWall, Width: 2, Height: 1})
	if !PlaceBuilding(wa.sim, e, 1, 2) {
		t.Fatal("2×1 放置应成功")
	}
	wg := ecs.Resource[worldmap.WalkGrid](wa.sim)
	if wg.Walkable(1, 2) || wg.Walkable(2, 2) {
		t.Fatal("2×1 建筑应占 (1,2) 与 (2,2)")
	}
	if wg.Walkable(3, 2) != true {
		t.Fatal("(3,2) 不应被占")
	}
	if !DemolishBuilding(wa.sim, e) {
		t.Fatal("拆除应成功")
	}
	if !wg.Walkable(1, 2) || !wg.Walkable(2, 2) {
		t.Fatal("拆除后两格都应恢复可走")
	}
}

// 移动碰撞：墙阻挡后，玩家移动命令无法穿墙。
func TestBuildingMoveCollision(t *testing.T) {
	wa := newBuildingWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 1, Y: 2})
	ecs.Set(wa.sim, player, components.Moveable{Interval: 1})

	wall := wa.sim.CreateEntity()
	ecs.Add(wa.sim, wall, components.Building{Kind: components.BuildingWall, Width: 1, Height: 1})
	if !PlaceBuilding(wa.sim, wall, 2, 2) {
		t.Fatal("墙放置失败")
	}
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1, DY: 0}})
	tickWorld(wa)
	p := ecs.Get[components.Position](wa.sim, player)
	if p.X != 1 || p.Y != 2 {
		t.Fatalf("撞墙应停下: (%d,%d)", p.X, p.Y)
	}
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 0, DY: 1}})
	tickWorld(wa)
	p = ecs.Get[components.Position](wa.sim, player)
	if p.X != 1 || p.Y != 3 {
		t.Fatalf("非阻挡方向应可走: (%d,%d)", p.X, p.Y)
	}
}

// 拆除：取消阻挡 + 实体销毁。
func TestBuildingDemolish(t *testing.T) {
	wa := newBuildingWorld(t)
	wall := wa.sim.CreateEntity()
	ecs.Add(wa.sim, wall, components.Building{Kind: components.BuildingWall, Width: 1, Height: 1})
	if !PlaceBuilding(wa.sim, wall, 2, 2) {
		t.Fatal("墙放置失败")
	}
	if !DemolishBuilding(wa.sim, wall) {
		t.Fatal("拆除应成功")
	}
	if wa.sim.IsAlive(wall) {
		t.Fatal("拆除后实体应销毁")
	}
	wg := ecs.Resource[worldmap.WalkGrid](wa.sim)
	if !wg.Walkable(2, 2) {
		t.Fatal("拆除后占格应恢复可走")
	}
}

// 命令拆分：build 只创建未放置实体，place 才放置；check 查询可放置。
func TestBuildPlaceCommands(t *testing.T) {
	wa := newBuildingWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandBuild, Data: BuildData{Player: player, Kind: components.BuildingCampfire}})
	var e ecs.Entity
	ecs.Query[components.Building](wa.sim, func(id ecs.Entity, _ *components.Building) { e = id })
	if e == 0 {
		t.Fatal("build 应创建建筑实体")
	}
	b := ecs.Get[components.Building](wa.sim, e)
	if b.Placed {
		t.Fatal("build 只创建，不应放置")
	}
	wg := ecs.Resource[worldmap.WalkGrid](wa.sim)
	if !wg.Walkable(2, 2) {
		t.Fatal("未放置不应阻挡")
	}
	if !CanPlaceBuilding(wg, 2, 3, 1, 1) {
		t.Fatal("空位应可放置")
	}
	if CanPlaceBuilding(wg, -1, 0, 1, 1) {
		t.Fatal("越界不应可放置")
	}

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandPlace, Data: PlaceData{Player: player, Entity: e, X: 2, Y: 3}})
	b = ecs.Get[components.Building](wa.sim, e)
	if !b.Placed || wg.Walkable(2, 3) {
		t.Fatal("place 应放置并阻挡")
	}
	if !ecs.Has[components.HeatSource](wa.sim, e) {
		t.Fatal("火堆放置后应挂 HeatSource")
	}
}
