package world

import (
	"os"
	"path/filepath"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// testM5Cfg 生成带模板 + 浆果丛 + 树 + 矿的测试配置。
func testM5Cfg(t *testing.T) WorldConfig {
	t.Helper()
	dir := t.TempDir()
	res := filepath.Join(dir, "resources.json")
	tmpl := filepath.Join(dir, "templates.json")
	if err := os.WriteFile(res, []byte(`[
		{"kind":"berry","x":0,"y":1,"action":"pick","work":3},
		{"kind":"wood","x":3,"y":0,"action":"chop","work":10},
		{"kind":"flint","x":4,"y":0,"action":"mine","work":3}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpl, []byte(`{
		"berry": {"name":"浆果","color":"#e2574c","stack_size":20,"use_effect":{"hunger":8}},
		"wood": {"name":"木头","color":"#9a6b3f","stack_size":20,"drop_table":[{"kind":"wood","count":2}]},
		"flint": {"name":"燧石","color":"#9aa0a8","stack_size":20,"drop_table":[{"kind":"flint","count":2}]},
		"axe": {"name":"斧头","color":"#c9a86a","stack_size":1,"tool":{"action":"chop","efficiency":5,"durability":10}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return WorldConfig{ResourcesPath: res, TemplatesPath: tmpl}
}

// TestTemplatesLoad：模板表加载 + 默认值补全。
func TestTemplatesLoad(t *testing.T) {
	wa := NewWorldActor(testM5Cfg(t))
	berry := wa.templates[components.ItemBerry]
	if berry.UseEffect == nil || berry.UseEffect.Hunger != 8 {
		t.Fatalf("berry use_effect = %+v, want hunger+8", berry.UseEffect)
	}
	if berry.StackSize != 20 {
		t.Fatalf("berry stack = %d, want 20", berry.StackSize)
	}
	if len(wa.templates[components.ItemWood].DropTable) != 1 {
		t.Fatalf("wood drop_table = %+v", wa.templates[components.ItemWood].DropTable)
	}
	axe := wa.templates[components.ItemAxe]
	if axe.Tool == nil || axe.Tool.Action != components.WorkChop || axe.Tool.Efficiency != 5 || axe.Tool.Durability != 10 {
		t.Fatalf("axe tool = %+v", axe.Tool)
	}
}

// TestUseBerry：吃浆果 → 背包消耗一个 + 饥饿恢复。
func TestUseBerry(t *testing.T) {
	cfg := testM5Cfg(t)
	cfg.HungerRate = 1
	eng, pid, wa, _ := newM5World(t, cfg)
	player := createPlayer(t, eng, pid, "u1")

	// 采一个浆果（浆果丛=实体1，出生点距离1）
	eng.Send(pid, Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: player, Target: 1}})
	eng.Send(pid, Tick{})
	// 饿 20 tick：饥饿 100 → 80
	for i := 0; i < 20; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemBerry].Count != 1 {
		t.Fatalf("采集后背包浆果 = %d, want 1", inv.Items[components.ItemBerry].Count)
	}

	eng.Send(pid, Command{UID: "u1", Kind: CommandUse, Data: UseData{Player: player, Kind: components.ItemBerry}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	inv = ecs.Get[components.Inventory](wa.sim, player)
	if _, ok := inv.Items[components.ItemBerry]; ok {
		t.Fatal("使用后背包不应再有浆果")
	}
	h := ecs.Get[components.Hunger](wa.sim, player)
	// 使用效果在 tick 开始应用（+8），随后系统再扣 1 → 100-20-1+8-1 = 86
	if h.Level != 86 {
		t.Fatalf("使用后饥饿 = %d, want 86", h.Level)
	}
}

// TestTreeDeathDropAndPickup：装备斧头砍树（效率 5×2）→ 死亡 → 就地掉落 → 拾取进背包。
func TestTreeDeathDropAndPickup(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid) // 等 createPlayer 处理完，避免测试直写 sim 与 actor 并发
	// 直接给一把斧头（模板耐久 10）
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Items[components.ItemAxe] = components.ItemStack{Kind: components.ItemAxe, Count: 1, MaxStack: 1, Durability: 10}

	// 树=实体2 在 (3,0)，玩家先走到 (2,0)
	eng.Send(pid, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 2, DY: 0}})
	eng.Send(pid, Tick{})
	// 装备斧头
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if !ecs.Has[components.Equipped](wa.sim, player) {
		t.Fatal("装备后应有 Equipped 组件")
	}
	// 砍 2 刀（效率 5，树 WorkLeft 10）→ 归零 → Dead → 掉落
	for i := 0; i < 2; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: 2}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if !ecs.Has[components.Dead](wa.sim, 2) {
		t.Fatal("树砍完应挂 Dead")
	}
	if !ecs.Has[components.Loot](wa.sim, 2) {
		t.Fatal("树死后应有 Loot")
	}
	loot := ecs.Get[components.Loot](wa.sim, 2)
	if len(loot.Items) != 1 || loot.Items[0].Kind != components.ItemWood || loot.Items[0].Count != 2 {
		t.Fatalf("掉落 = %+v, want wood x2", loot.Items)
	}
	if ecs.Has[components.Workable](wa.sim, 2) {
		t.Fatal("掉落转化后不应再可交互")
	}
	// 斧头耐久 10 - 2 = 8
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemAxe].Durability != 8 {
		t.Fatalf("斧头耐久 = %d, want 8", inv.Items[components.ItemAxe].Durability)
	}

	// 拾取
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: 2}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if wa.sim.IsAlive(2) {
		t.Fatal("拾取后掉落物实体应销毁")
	}
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemWood].Count != 2 {
		t.Fatalf("拾取后背包木头 = %d, want 2", inv.Items[components.ItemWood].Count)
	}
}

// TestWorkActionMismatch：拿斧头挖矿 → 拒绝（工具动作不匹配），矿不消耗。
func TestWorkActionMismatch(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Items[components.ItemAxe] = components.ItemStack{Kind: components.ItemAxe, Count: 1, MaxStack: 1, Durability: 10}

	// 矿=实体3 在 (4,0)，走到 (3,0)（距离1）
	eng.Send(pid, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 3, DY: 0}})
	eng.Send(pid, Tick{})
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	eng.Send(pid, Tick{})
	eng.Send(pid, Command{UID: "u1", Kind: CommandMine, Data: MineData{Player: player, Target: 3}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	w := ecs.Get[components.Workable](wa.sim, 3)
	if w.WorkLeft != 3 {
		t.Fatalf("斧头挖矿不应生效：WorkLeft = %d, want 3", w.WorkLeft)
	}
	if ecs.Has[components.Dead](wa.sim, 3) {
		t.Fatal("矿不应死亡")
	}
}

// TestPickupTooFar：距离不够不能拾取。
func TestPickupTooFar(t *testing.T) {
	cfg := testM5Cfg(t)
	cfg.ResourcesPath = filepath.Join(t.TempDir(), "nope.json") // 无资源 seed，树手动建
	eng, pid, wa, _ := newM5World(t, cfg)
	player := createPlayer(t, eng, pid, "u1")
	tree := wa.sim.CreateEntity()
	ecs.Add(wa.sim, tree, components.Position{X: 1, Y: 0})
	ecs.Add(wa.sim, tree, components.Workable{Kind: components.ItemWood, Action: components.WorkChop, WorkLeft: 1, MaxWork: 1})

	// 砍死（徒手效率 1，距离 1 合法）→ 掉落
	eng.Send(pid, Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: tree}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if !ecs.Has[components.Loot](wa.sim, tree) {
		t.Fatal("预期树死亡产生掉落")
	}

	// 走远再拾取 → 距离不够
	eng.Send(pid, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 4, DY: 0}})
	eng.Send(pid, Tick{})
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: tree}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if !wa.sim.IsAlive(tree) {
		t.Fatal("距离不够不应拾取成功")
	}
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if len(inv.Items) != 0 {
		t.Fatalf("距离不够不应进背包, got %v", inv.Items)
	}
}
