package world

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/config"
)

// newBuildingWorld 带地图的世界（5×5 全可走 + MapData 资源）。
func newBuildingWorld(t *testing.T) *WorldActor {
	t.Helper()
	wa := NewWorldActor(WorldConfig{})
	md := &MapData{Width: 5, Height: 5, CornerTypes: make([]byte, 6*6)}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = byte(3) // GRASS
	}
	wa.sim.AddResource(md)
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
	md := ecs.Resource[MapData](wa.sim)
	if md.Walkable(2, 2) {
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

// 建筑模板配置：buildings.json 的占格尺寸流入建造请求创建的实体。
func TestBuildingConfigDimensions(t *testing.T) {
	gc, err := config.LoadGameConfig(WorldConfig{BuildingsPath: "../../../configs/buildings.json"})
	if err != nil {
		t.Fatal(err)
	}
	tpl, ok := gc.Buildings[components.BuildingCampfire]
	if !ok || tpl.Width != 2 || tpl.Height != 2 {
		t.Fatalf("campfire 模板 = %+v, want 2×2", tpl)
	}
	wa := NewWorldActorWithConfig(WorldConfig{}, gc)
	wa.createPlayer("u1")
	res := wa.cmds.build("u1", components.BuildingCampfire)
	if !res.Started {
		t.Fatalf("build 应成功: %s", res.Message)
	}
	b := ecs.Get[components.Building](wa.sim, res.Entity)
	if b.Width != 2 || b.Height != 2 {
		t.Fatalf("实体尺寸 = %d×%d, want 2×2", b.Width, b.Height)
	}
	if b.Placed {
		t.Fatal("build 只创建，不应放置")
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
	md := ecs.Resource[MapData](wa.sim)
	if md.Walkable(1, 2) || md.Walkable(2, 2) {
		t.Fatal("2×1 建筑应占 (1,2) 与 (2,2)")
	}
	if !md.Walkable(3, 2) {
		t.Fatal("(3,2) 不应被占")
	}
	if !DemolishBuilding(wa.sim, e) {
		t.Fatal("拆除应成功")
	}
	if !md.Walkable(1, 2) || !md.Walkable(2, 2) {
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
	md := ecs.Resource[MapData](wa.sim)
	if !md.Walkable(2, 2) {
		t.Fatal("拆除后占格应恢复可走")
	}
}

// 生命周期：直接移除 Block 组件也应清除占格（不销毁实体）。
func TestBlockLifecycleRemove(t *testing.T) {
	wa := newBuildingWorld(t)
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Building{Kind: components.BuildingWall, Width: 1, Height: 1})
	if !PlaceBuilding(wa.sim, e, 2, 2) {
		t.Fatal("墙放置失败")
	}
	md := ecs.Resource[MapData](wa.sim)
	if md.Walkable(2, 2) {
		t.Fatal("放置后应阻挡")
	}
	ecs.Remove[components.Block](wa.sim, e)
	if !md.Walkable(2, 2) {
		t.Fatal("移除 Block 组件后应恢复可走")
	}
}

// 命令拆分：build 请求只创建未放置实体，place 才放置；check 查询可放置。
func TestBuildPlaceCommands(t *testing.T) {
	wa := newBuildingWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})

	res := wa.cmds.build("u1", components.BuildingCampfire)
	if !res.Started {
		t.Fatalf("build 应成功: %s", res.Message)
	}
	e := res.Entity
	if e == 0 || !wa.sim.IsAlive(e) {
		t.Fatal("build 应返回存活建筑实体")
	}
	b := ecs.Get[components.Building](wa.sim, e)
	if b.Placed {
		t.Fatal("build 只创建，不应放置")
	}
	md := ecs.Resource[MapData](wa.sim)
	if !md.Walkable(2, 2) {
		t.Fatal("未放置不应阻挡")
	}
	if !CanPlaceBuilding(md, 2, 3, 1, 1) {
		t.Fatal("空位应可放置")
	}
	if CanPlaceBuilding(md, -1, 0, 1, 1) {
		t.Fatal("越界不应可放置")
	}

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandPlace, Data: PlaceData{Actor: player, Entity: e, X: 2, Y: 3}})
	b = ecs.Get[components.Building](wa.sim, e)
	if !b.Placed || md.Walkable(2, 3) {
		t.Fatal("place 应放置并阻挡")
	}
	if !ecs.Has[components.HeatSource](wa.sim, e) {
		t.Fatal("火堆放置后应挂 HeatSource")
	}
}

// 存档恢复：已放置建筑的 Block 阻挡层要随实体一起重建（MapData 与实体保持一致）。
func TestSaveLoadRestoresBlocks(t *testing.T) {
	cfg := WorldConfig{
		MapPath:    "../../../configs/map.json",
		BiomesPath: "../../../configs/biomes.json",
	}
	eng1, pid1, wa1, _ := newM5World(t, cfg)

	// 找一块 2×2 全可走的地放火堆
	md1 := ecs.Resource[MapData](wa1.sim)
	x, y := -1, -1
	for yy := 0; yy < md1.Height && x < 0; yy++ {
		for xx := 0; xx < md1.Width; xx++ {
			if md1.AllWalkable(xx, yy, 2, 2) {
				x, y = xx, yy
				break
			}
		}
	}
	if x < 0 {
		t.Fatal("地图应有可放置 2×2 的位置")
	}
	e := wa1.sim.CreateEntity()
	ecs.Add(wa1.sim, e, components.Building{Kind: components.BuildingCampfire, Width: 2, Height: 2})
	if !PlaceBuilding(wa1.sim, e, x, y) {
		t.Fatalf("放置 (%d,%d) 应成功", x, y)
	}
	eng1.Send(pid1, Tick{})

	resp := eng1.Request(pid1, SaveRequest{}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	data := v.([]byte)

	// 加载目标世界不带地图配置：实体/地图全部由存档恢复（避免与种子实体 id 冲突）
	eng2, pid2, wa2, _ := newM5World(t, WorldConfig{})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}
	md2 := ecs.Resource[MapData](wa2.sim)
	if md2.Width == 0 {
		t.Fatal("存档应恢复地图")
	}
	if md2.Walkable(x, y) || md2.Walkable(x+1, y+1) {
		t.Fatal("存档恢复后火堆占格应重新阻挡")
	}
	eng2.Send(pid2, Tick{})
}
