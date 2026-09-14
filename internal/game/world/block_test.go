package world

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/worldmap"
)

// 占位物统一走 Block（占位 ≠ 不可走）：
//   - 树/岩按模板 collision_radius 挂"格心圆"Block：格子照样可走，只影响寻路代价；
//   - 浆果/花/灌木不占位；
//   - 工作站/雕像挂"占格盒"Block（同样可走，但寻路代价高、放置冲突）；
//   - 出生点安全区没有任何占位物。
func TestSeedBlockerResources(t *testing.T) {
	wa := NewWorldActor(WorldConfig{
		TemplatesPath: "../../../configs/resource_templates.json",
		MapPath:       "../../../configs/map.json",
		BiomesPath:    "../../../configs/biomes.json",
	})
	md := ecs.Resource[MapData](wa.sim)

	var wood, flint, berry, flower, shrub ecs.Entity
	var woodPos, flowerPos, shrubPos components.Position
	ecs.Query2[interactive.Choppable, components.Position](wa.sim, func(e ecs.Entity, w *interactive.Choppable, p *components.Position) {
		if w.Kind == components.ItemWood && wood == 0 {
			wood, woodPos = e, *p
		}
	})
	ecs.Query2[interactive.Minable, components.Position](wa.sim, func(e ecs.Entity, w *interactive.Minable, p *components.Position) {
		if w.Kind == components.ItemFlint && flint == 0 {
			flint = e
		}
	})
	ecs.Query2[interactive.Pickable, components.Position](wa.sim, func(e ecs.Entity, w *interactive.Pickable, p *components.Position) {
		if w.Kind == components.ItemBerry && berry == 0 {
			berry = e
		}
		if w.Kind == components.ItemFlower && flower == 0 {
			flower, flowerPos = e, *p
		}
	})
	ecs.Query2[components.Scenery, components.Position](wa.sim, func(e ecs.Entity, scenery *components.Scenery, p *components.Position) {
		if scenery.Kind == components.ItemShrub && shrub == 0 {
			shrub, shrubPos = e, *p
		}
	})
	if wood == 0 || flint == 0 || berry == 0 || flower == 0 || shrub == 0 {
		t.Fatal("地图应生成树/岩/浆果/花/灌木")
	}
	if !ecs.Has[components.Block](wa.sim, wood) || !ecs.Has[components.Block](wa.sim, flint) {
		t.Fatal("树/岩应挂 Block（占位 + 形状）")
	}
	if radius := ecs.Get[components.Block](wa.sim, wood).Radius; radius <= 0 || radius >= 0.5 {
		t.Fatalf("树应是格心圆（半径在 (0,0.5)），得到 %v", radius)
	}
	if ecs.Has[components.Block](wa.sim, berry) {
		t.Fatal("浆果不应占位")
	}
	if ecs.Has[components.Block](wa.sim, flower) || !md.Walkable(flowerPos.X, flowerPos.Y) {
		t.Fatal("花不应阻挡")
	}
	if ecs.Has[components.Block](wa.sim, shrub) || !md.Walkable(shrubPos.X, shrubPos.Y) {
		t.Fatal("灌木不应阻挡")
	}
	if ecs.Has[interactive.Pickable](wa.sim, shrub) ||
		ecs.Has[interactive.Choppable](wa.sim, shrub) ||
		ecs.Has[interactive.Minable](wa.sim, shrub) {
		t.Fatal("灌木不应携带交互能力")
	}
	if ecs.Has[components.DropSource](wa.sim, shrub) {
		t.Fatal("灌木不应携带掉落来源")
	}
	if !md.Walkable(woodPos.X, woodPos.Y) {
		t.Fatal("树所在格应可走（占位 ≠ 不可走，只有水/悬崖才不可走）")
	}
	if got := md.OccupiedCostAt(woodPos.X, woodPos.Y); got != worldmap.OccupiedCostThin {
		t.Fatalf("树所在格占位代价 = %d, want %d（绕开更划算）", got, worldmap.OccupiedCostThin)
	}
	if wa.blockers.Len() == 0 {
		t.Fatal("形状索引应有树/岩")
	}
	ecs.Query2[components.Workstation, components.Position](wa.sim, func(
		e ecs.Entity,
		_ *components.Workstation,
		p *components.Position,
	) {
		block := ecs.Get[components.Block](wa.sim, e)
		if block.Radius != 0 || block.Width < 1 || block.Height < 1 {
			t.Fatalf("工作站 %d 应是占格盒 Block, got %+v", e, *block)
		}
		if !md.Walkable(p.X, p.Y) {
			t.Fatalf("工作站 %d @(%d,%d) 所在格地形应可走（占位不等于不可走）", e, p.X, p.Y)
		}
		if got := md.OccupiedCostAt(p.X, p.Y); got != worldmap.OccupiedCostFull {
			t.Fatalf("工作站 %d 占位代价 = %d, want %d", e, got, worldmap.OccupiedCostFull)
		}
	})

	// 出生点安全区：出生点曼哈顿 ≤ 3 内没有任何占位物（map.json spawn 64,64）
	checkSpawnClear := func(p *components.Position, what string) {
		dx, dy := p.X-64, p.Y-64
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx+dy <= 3 {
			t.Fatalf("出生点附近不应有%s: (%d,%d)", what, p.X, p.Y)
		}
	}
	ecs.Query2[components.Block, components.Position](wa.sim, func(e ecs.Entity, _ *components.Block, p *components.Position) {
		checkSpawnClear(p, "占位物")
	})
}

// 砍倒树：转为掉落物时解除 Block，形状与占位代价都注销。
func TestChopClearsTreeBlocker(t *testing.T) {
	wa := NewWorldActor(WorldConfig{
		TemplatesPath: "../../../configs/resource_templates.json",
		MapPath:       "../../../configs/map.json",
		BiomesPath:    "../../../configs/biomes.json",
	})
	var tree ecs.Entity
	var treePos components.Position
	ecs.Query2[interactive.Choppable, components.Position](wa.sim, func(e ecs.Entity, w *interactive.Choppable, p *components.Position) {
		if w.Kind == components.ItemWood && tree == 0 {
			tree, treePos = e, *p
		}
	})
	if tree == 0 {
		t.Fatal("应生成树")
	}
	md := ecs.Resource[MapData](wa.sim)
	if !ecs.Has[components.Block](wa.sim, tree) {
		t.Fatal("树应占位（Block）")
	}
	blockersBefore := wa.blockers.Len()
	if !md.IsOccupied(treePos.X, treePos.Y) {
		t.Fatal("树所在格应标记占位")
	}
	w := ecs.Get[interactive.Choppable](wa.sim, tree)
	w.WorkLeft = 1 // 一刀砍倒（裸手效率 1）

	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: treePos.X + 1, Y: treePos.Y})
	// 砍伐需要工具：给玩家一把斧头并装备
	inv := ecs.Ensure[components.Inventory](wa.sim, player)
	inv.Add(components.ItemAxe, 1, 1, 10)
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: tree}})
	runActionTicks(wa, 5)
	wa.processDrops()

	if wa.sim.IsAlive(tree) {
		t.Fatal("砍倒后资源来源应销毁")
	}
	if wa.blockers.Len() != blockersBefore-1 {
		t.Fatalf("砍倒后形状应注销, 之前 %d 之后 %d", blockersBefore, wa.blockers.Len())
	}
	if md.IsOccupied(treePos.X, treePos.Y) {
		t.Fatal("砍倒后占位应清除")
	}
	if loot := findLootableKind(t, wa, components.ItemWood); loot == tree {
		t.Fatal("砍倒后应创建独立掉落物")
	}
}

// 存档迁移：占位机制归一化——旧档给树挂的整格 Block 改成模板半径的格心圆，
// 旧档建筑（没挂 Block）补上占格盒；两者所在格地形都仍然可走。
func TestSaveLoadMigratesBlockers(t *testing.T) {
	// 旧档模拟：world1 不带模板配置 → 种子树/岩不挂 Block
	cfg1 := WorldConfig{
		MapPath:    "../../../configs/map.json",
		BiomesPath: "../../../configs/biomes.json",
	}
	eng1, pid1, wa1, _ := newM5World(t, cfg1)

	var tree ecs.Entity
	var treePos components.Position
	ecs.Query2[interactive.Choppable, components.Position](wa1.sim, func(e ecs.Entity, w *interactive.Choppable, p *components.Position) {
		if w.Kind == components.ItemWood && tree == 0 {
			tree, treePos = e, *p
		}
	})
	if tree == 0 || ecs.Has[components.Block](wa1.sim, tree) {
		t.Fatal("旧档树应存在且无 Block")
	}
	// 上一版行为：树占整格 → 旧档里带整格 Block（迁移应把它改成格心圆）
	ecs.Add(wa1.sim, tree, components.Block{Width: 1, Height: 1})

	// 手摆一个已放置建筑（无 Block，模拟旧档）
	md1 := ecs.Resource[MapData](wa1.sim)
	bx, by := -1, -1
	for yy := 0; yy < md1.Height && bx < 0; yy++ {
		for xx := 0; xx < md1.Width; xx++ {
			if md1.AllWalkable(xx, yy, 1, 1) && !(xx == treePos.X && yy == treePos.Y) {
				bx, by = xx, yy
				break
			}
		}
	}
	if bx < 0 {
		t.Fatal("应有可放置位置")
	}
	b := wa1.sim.CreateEntity()
	ecs.Add(wa1.sim, b, components.Building{Kind: components.BuildingWall, Width: 1, Height: 1, Placed: true})
	ecs.Add(wa1.sim, b, components.Position{X: bx, Y: by})
	if ecs.Has[components.Block](wa1.sim, b) {
		t.Fatal("旧档建筑不应有 Block")
	}
	eng1.Send(pid1, Tick{})

	resp := eng1.Request(pid1, SaveRequest{}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	data := v.([]byte)

	// 新世界带模板配置加载：迁移逻辑用模板识别阻挡类环境物
	_, _, wa2, _ := newM5World(t, WorldConfig{TemplatesPath: "../../../configs/resource_templates.json"})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}

	var b2 ecs.Entity
	var bPos components.Position
	ecs.Query2[components.Building, components.Position](wa2.sim, func(e ecs.Entity, bb *components.Building, p *components.Position) {
		if bb.Kind == components.BuildingWall {
			b2, bPos = e, *p
		}
	})
	if b2 == 0 {
		t.Fatal("旧档建筑应存在")
	}
	if block := ecs.Get[components.Block](wa2.sim, b2); block.Radius != 0 || block.Width != 1 {
		t.Fatalf("旧档建筑应迁移成占格盒 Block, got %+v", *block)
	}

	var t2 ecs.Entity
	var tPos components.Position
	ecs.Query2[interactive.Choppable, components.Position](wa2.sim, func(e ecs.Entity, w *interactive.Choppable, p *components.Position) {
		if w.Kind == components.ItemWood {
			t2, tPos = e, *p
		}
	})
	if t2 == 0 {
		t.Fatal("旧档树应存在")
	}
	if block := ecs.Get[components.Block](wa2.sim, t2); block.Radius <= 0 {
		t.Fatalf("旧档树应迁移成格心圆 Block（Radius>0）, got %+v", *block)
	}

	md2 := ecs.Resource[MapData](wa2.sim)
	if !md2.Walkable(tPos.X, tPos.Y) {
		t.Fatal("迁移后树所在格应可走（占位不等于不可走）")
	}
	if got := md2.OccupiedCostAt(tPos.X, tPos.Y); got != worldmap.OccupiedCostThin {
		t.Fatalf("迁移后树所在格占位代价 = %d, want %d", got, worldmap.OccupiedCostThin)
	}
	if !md2.IsOccupied(bPos.X, bPos.Y) {
		t.Fatal("迁移后建筑应占位")
	}
}
