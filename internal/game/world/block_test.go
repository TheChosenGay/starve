package world

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// 树/岩按模板挂 Block 占格；浆果不阻挡；出生点安全区无阻挡物。
func TestSeedBlockingResources(t *testing.T) {
	wa := NewWorldActor(WorldConfig{
		TemplatesPath: "../../../configs/resource_templates.json",
		MapPath:       "../../../configs/map.json",
		BiomesPath:    "../../../configs/biomes.json",
	})
	md := ecs.Resource[MapData](wa.sim)

	var wood, flint, berry ecs.Entity
	var woodPos components.Position
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
	})
	if wood == 0 || flint == 0 || berry == 0 {
		t.Fatal("地图应生成树/岩/浆果")
	}
	if !ecs.Has[components.Block](wa.sim, wood) || !ecs.Has[components.Block](wa.sim, flint) {
		t.Fatal("树/岩应挂 Block")
	}
	if ecs.Has[components.Block](wa.sim, berry) {
		t.Fatal("浆果不应阻挡")
	}
	if md.Walkable(woodPos.X, woodPos.Y) {
		t.Fatal("树所在格应阻挡")
	}
	ecs.Query2[components.Workstation, components.Position](wa.sim, func(
		e ecs.Entity,
		_ *components.Workstation,
		p *components.Position,
	) {
		if !ecs.Has[components.Block](wa.sim, e) || md.Walkable(p.X, p.Y) {
			t.Fatalf("工作站 %d @(%d,%d) 应占格阻挡", e, p.X, p.Y)
		}
	})

	// 出生点安全区：无阻挡物在出生点曼哈顿 ≤ 3（map.json spawn 64,64）
	ecs.Query2[components.Block, components.Position](wa.sim, func(e ecs.Entity, _ *components.Block, p *components.Position) {
		dx, dy := p.X-64, p.Y-64
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx+dy <= 3 {
			t.Fatalf("出生点附近不应有阻挡物: (%d,%d)", p.X, p.Y)
		}
	})
}

// 砍倒树：转为掉落物时解除 Block，占格恢复可走。
func TestChopUnblocksTree(t *testing.T) {
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
	if md.Walkable(treePos.X, treePos.Y) {
		t.Fatal("树应占格")
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
	if !md.Walkable(treePos.X, treePos.Y) {
		t.Fatal("砍倒后占格应恢复可走")
	}
	if loot := findLootableKind(t, wa, components.ItemWood); loot == tree {
		t.Fatal("砍倒后应创建独立掉落物")
	}
}

// 存档迁移：Block 机制之前的旧档（建筑/树无 Block）加载后自动补挂并阻挡。
func TestSaveLoadMigratesBlocks(t *testing.T) {
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
	if b2 == 0 || !ecs.Has[components.Block](wa2.sim, b2) {
		t.Fatal("旧档建筑应迁移补挂 Block")
	}

	var t2 ecs.Entity
	var tPos components.Position
	ecs.Query2[interactive.Choppable, components.Position](wa2.sim, func(e ecs.Entity, w *interactive.Choppable, p *components.Position) {
		if w.Kind == components.ItemWood {
			t2, tPos = e, *p
		}
	})
	if t2 == 0 || !ecs.Has[components.Block](wa2.sim, t2) {
		t.Fatal("旧档树应迁移补挂 Block")
	}

	md2 := ecs.Resource[MapData](wa2.sim)
	if md2.Walkable(tPos.X, tPos.Y) {
		t.Fatal("迁移后树应阻挡")
	}
	if md2.Walkable(bPos.X, bPos.Y) {
		t.Fatal("迁移后建筑应阻挡")
	}
}
