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

// 放置：补 Position + placed=true + WalkGrid 逐格阻挡；重复放置/不可走拒绝。
func TestBuildingPlace(t *testing.T) {
	wa := newBuildingWorld(t)
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Building{Kind: components.BuildingCampfire, Size: 1})

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
	// 重复放置拒绝
	if PlaceBuilding(wa.sim, e, 3, 3) {
		t.Fatal("已放置建筑不应重复放置")
	}
	// 占格上再放别的建筑拒绝
	e2 := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e2, components.Building{Kind: components.BuildingWall, Size: 1})
	if PlaceBuilding(wa.sim, e2, 2, 2) {
		t.Fatal("被占格不应再放置")
	}
}

// 移动碰撞：墙阻挡后，玩家移动命令无法穿墙。
func TestBuildingMoveCollision(t *testing.T) {
	wa := newBuildingWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 1, Y: 2})
	ecs.Set(wa.sim, player, components.Moveable{Interval: 1})

	wall := wa.sim.CreateEntity()
	ecs.Add(wa.sim, wall, components.Building{Kind: components.BuildingWall, Size: 1})
	if !PlaceBuilding(wa.sim, wall, 2, 2) {
		t.Fatal("墙放置失败")
	}

	// 朝墙走（右）：位置不变
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1, DY: 0}})
	tickWorld(wa)
	p := ecs.Get[components.Position](wa.sim, player)
	if p.X != 1 || p.Y != 2 {
		t.Fatalf("撞墙应停下: (%d,%d)", p.X, p.Y)
	}
	// 绕开走（下）：可移动
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
	ecs.Add(wa.sim, wall, components.Building{Kind: components.BuildingWall, Size: 1})
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

// 建造命令：玩家在脚下建火堆 → 实体挂 Building(placed) + HeatSource。
func TestBuildCommand(t *testing.T) {
	wa := newBuildingWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandBuild, Data: BuildData{Player: player, Kind: components.BuildingCampfire, X: 2, Y: 3}})
	found := false
	ecs.Query[components.Building](wa.sim, func(e ecs.Entity, b *components.Building) {
		if b.Kind == components.BuildingCampfire && b.Placed {
			found = true
			if !ecs.Has[components.HeatSource](wa.sim, e) {
				t.Fatal("火堆应挂 HeatSource")
			}
		}
	})
	if !found {
		t.Fatal("建造命令应生成已放置的火堆")
	}
}
