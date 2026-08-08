package world

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/pkg/proto"
)

// testM5Cfg 生成带模板 + 浆果丛 + 树 + 矿的测试配置。
func testM5Cfg(t *testing.T) WorldConfig {
	t.Helper()
	dir := t.TempDir()
	res := filepath.Join(dir, "resources.json")
	tmpl := filepath.Join(dir, "templates.json")
	craft := filepath.Join(dir, "crafting.json")
	stn := filepath.Join(dir, "stations.json")
	if err := os.WriteFile(res, []byte(`[
		{"kind":"berry","x":0,"y":1,"action":"pick","work":3},
		{"kind":"wood","x":3,"y":0,"action":"chop","work":10},
		{"kind":"flint","x":4,"y":0,"action":"mine","work":3}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmpl, []byte(`{
		"berry": {"name":"浆果","color":"#e2574c","stack_size":20,"use_effect":{"hunger":8},"respawn_ticks":5},
		"wood": {"name":"木头","color":"#9a6b3f","stack_size":20,"drop_table":[{"kind":"wood","count":2}]},
		"flint": {"name":"燧石","color":"#9aa0a8","stack_size":20,"drop_table":[{"kind":"flint","count":2}]},
		"axe": {"name":"斧头","color":"#c9a86a","stack_size":1,"tool":{"action":"chop","efficiency":5,"durability":10}}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(craft, []byte(`{"recipes":[
		{"id":"axe","workstation":"campfire","ticks":3,"output":{"kind":"axe","count":1},
		 "ingredients":[{"kind":"wood","count":3},{"kind":"flint","count":1}]},
		{"id":"pickaxe","workstation":"workbench","ticks":3,"output":{"kind":"pickaxe","count":1},
		 "ingredients":[{"kind":"wood","count":2},{"kind":"flint","count":2}]}
	]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stn, []byte(`[{"type":"campfire","x":1,"y":1},{"type":"workbench","x":1,"y":2}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	return WorldConfig{ResourcesPath: res, TemplatesPath: tmpl, RecipesPath: craft, StationsPath: stn}
}

// findWorkable 按 kind 找第一个 Workable 实体（工作站 seed 后实体号不固定，测试不硬编码）。
func findWorkable(t *testing.T, wa *WorldActor, kind components.ItemKind) ecs.Entity {
	t.Helper()
	var found ecs.Entity
	ecs.Query[components.Workable](wa.sim, func(e ecs.Entity, w *components.Workable) {
		if found == 0 && w.Kind == kind {
			found = e
		}
	})
	if found == 0 {
		t.Fatalf("找不到 kind=%d 的 Workable", kind)
	}
	return found
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
	bush := findWorkable(t, wa, components.ItemBerry)
	eng.Send(pid, Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: player, Target: bush}})
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

	// 树（wood）在 (3,0)，玩家先走到 (2,0)
	tree := findWorkable(t, wa, components.ItemWood)
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
		eng.Send(pid, Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: tree}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if !ecs.Has[components.Dead](wa.sim, tree) {
		t.Fatal("树砍完应挂 Dead")
	}
	if !ecs.Has[components.Loot](wa.sim, tree) {
		t.Fatal("树死后应有 Loot")
	}
	loot := ecs.Get[components.Loot](wa.sim, tree)
	if len(loot.Items) != 1 || loot.Items[0].Kind != components.ItemWood || loot.Items[0].Count != 2 {
		t.Fatalf("掉落 = %+v, want wood x2", loot.Items)
	}
	if ecs.Has[components.Workable](wa.sim, tree) {
		t.Fatal("掉落转化后不应再可交互")
	}
	// 斧头耐久 10 - 2 = 8
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemAxe].Durability != 8 {
		t.Fatalf("斧头耐久 = %d, want 8", inv.Items[components.ItemAxe].Durability)
	}

	// 拾取
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: tree}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if wa.sim.IsAlive(tree) {
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

	// 矿（flint）在 (4,0)，走到 (3,0)（距离1）
	flint := findWorkable(t, wa, components.ItemFlint)
	eng.Send(pid, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 3, DY: 0}})
	eng.Send(pid, Tick{})
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	eng.Send(pid, Tick{})
	eng.Send(pid, Command{UID: "u1", Kind: CommandMine, Data: MineData{Player: player, Target: flint}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	w := ecs.Get[components.Workable](wa.sim, flint)
	if w.WorkLeft != 3 {
		t.Fatalf("斧头挖矿不应生效：WorkLeft = %d, want 3", w.WorkLeft)
	}
	if ecs.Has[components.Dead](wa.sim, flint) {
		t.Fatal("矿不应死亡")
	}
}

// TestAttackDeadTarget：攻击尸体被拒绝，且血量 clamp 到 0 不为负。
func TestAttackDeadTarget(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	u1 := createPlayer(t, eng, pid, "u1")
	u2 := createPlayer(t, eng, pid, "u2")
	eng.Send(pid, Command{UID: "u2", Kind: CommandMove, Data: MoveData{Entity: u2, DX: 1, DY: 0}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	// 打 10 次 → hp 0 → Dead
	for i := 0; i < 10; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: u1, Target: u2}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	hp := ecs.Get[components.Health](wa.sim, u2)
	if hp.Cur != 0 {
		t.Fatalf("u2 hp = %d, want 0", hp.Cur)
	}
	if !ecs.Has[components.Dead](wa.sim, u2) {
		t.Fatal("u2 应死亡")
	}

	// 再攻击尸体 → 无效，hp 保持 0
	eng.Send(pid, Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: u1, Target: u2}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	hp = ecs.Get[components.Health](wa.sim, u2)
	if hp.Cur != 0 {
		t.Fatalf("攻击尸体后 hp = %d, want 0", hp.Cur)
	}
}

// TestCorpseCleanup：超过保留时长尸体销毁。
func TestCorpseCleanup(t *testing.T) {
	cfg := testM5Cfg(t)
	cfg.CorpseRetentionTicks = 2
	eng, pid, wa, _ := newM5World(t, cfg)
	u1 := createPlayer(t, eng, pid, "u1")
	u2 := createPlayer(t, eng, pid, "u2")
	eng.Send(pid, Command{UID: "u2", Kind: CommandMove, Data: MoveData{Entity: u2, DX: 1, DY: 0}})
	eng.Send(pid, Tick{})
	for i := 0; i < 10; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: u1, Target: u2}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	if !wa.sim.IsAlive(u2) {
		t.Fatal("清理前 u2 应还在")
	}

	// 死亡盖戳后 +2 tick 即到期
	for i := 0; i < 3; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	if wa.sim.IsAlive(u2) {
		t.Fatal("超过保留时长尸体应被销毁")
	}
}

// TestBushRespawn：浆果丛采空挂 Respawn，RespawnSystem 到点恢复工作量。
func TestBushRespawn(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	bush := findWorkable(t, wa, components.ItemBerry) // 浆果丛（模板 respawn_ticks=5）

	// 采空（work 3）
	for i := 0; i < 3; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: player, Target: bush}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	w := ecs.Get[components.Workable](wa.sim, bush)
	if w.WorkLeft != 0 {
		t.Fatalf("采空后 WorkLeft = %d, want 0", w.WorkLeft)
	}
	if !ecs.Has[components.Respawn](wa.sim, bush) {
		t.Fatal("采空后应挂 Respawn 标记")
	}

	// 5 tick 后重生恢复
	for i := 0; i < 5; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	if ecs.Has[components.Respawn](wa.sim, bush) {
		t.Fatal("重生后 Respawn 标记应移除")
	}
	w = ecs.Get[components.Workable](wa.sim, bush)
	if w.WorkLeft != w.MaxWork {
		t.Fatalf("重生后 WorkLeft = %d, want %d", w.WorkLeft, w.MaxWork)
	}
}

// TestDrop：丢弃物品 → 背包减少、玩家位置生成 Loot，可再拾取还原。
func TestDrop(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)

	// 先采 2 个浆果
	bush := findWorkable(t, wa, components.ItemBerry)
	for i := 0; i < 2; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: player, Target: bush}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemBerry].Count != 2 {
		t.Fatalf("采集后浆果 = %d, want 2", inv.Items[components.ItemBerry].Count)
	}

	// 丢弃 1 个
	eng.Send(pid, Command{UID: "u1", Kind: CommandDrop,
		Data: DropData{Player: player, Kind: components.ItemBerry, Count: 1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemBerry].Count != 1 {
		t.Fatalf("丢弃后浆果 = %d, want 1", inv.Items[components.ItemBerry].Count)
	}

	// 找到掉落物实体并拾取回来
	lootEntity := ecs.Entity(0)
	ecs.Query[components.Loot](wa.sim, func(e ecs.Entity, l *components.Loot) {
		lootEntity = e
	})
	if lootEntity == 0 {
		t.Fatal("应有掉落物实体")
	}
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: lootEntity}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemBerry].Count != 2 {
		t.Fatalf("拾取后浆果 = %d, want 2", inv.Items[components.ItemBerry].Count)
	}
}

// TestDropInsufficient：丢弃数量超过背包 → 无效果。
func TestDropInsufficient(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	eng.Send(pid, Command{UID: "u1", Kind: CommandDrop,
		Data: DropData{Player: player, Kind: components.ItemBerry, Count: 99}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if len(inv.Items) != 0 {
		t.Fatalf("丢弃不足不应生效, got %v", inv.Items)
	}
	n := 0
	ecs.Query[components.Loot](wa.sim, func(e ecs.Entity, l *components.Loot) { n++ })
	if n != 0 {
		t.Fatalf("不应生成掉落物, got %d", n)
	}
}

// TestCraftAxe：材料 + 工作站就绪 → 制作开始（扣材料、挂 Crafting）→ 到点产出斧头 + 推送 done。
func TestCraftAxe(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Items[components.ItemWood] = components.ItemStack{Kind: components.ItemWood, Count: 3, MaxStack: 20}
	inv.Items[components.ItemFlint] = components.ItemStack{Kind: components.ItemFlint, Count: 1, MaxStack: 20}

	resp := eng.Request(pid, CraftRequest{UID: "u1", RecipeID: "axe"}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	r := v.(CraftResult)
	if !r.Started || r.Ticks != 3 {
		t.Fatalf("craft result = %+v", r)
	}
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemWood].Count != 0 || inv.Items[components.ItemFlint].Count != 0 {
		t.Fatalf("材料应已消耗: %v", inv.Items)
	}
	if !ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("应有 Crafting 组件")
	}

	for i := 0; i < 3; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, player)
	axe := inv.Items[components.ItemAxe]
	if axe.Count != 1 || axe.Durability != 10 {
		t.Fatalf("制作后斧头 = %+v, want count=1 durability=10", axe)
	}
	if ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("完成后 Crafting 应移除")
	}
	for _, ef := range pushed() {
		if ef.Route == proto.RouteCraftDone {
			if cd, ok := ef.Payload.(*proto.CraftDone); ok && cd.Uid == "u1" && cd.Success {
				return
			}
		}
	}
	t.Fatal("未收到 craft.done 推送")
}

// TestCraftWorkstationMissing：工作站不在附近 → 拒绝。
func TestCraftWorkstationMissing(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Items[components.ItemWood] = components.ItemStack{Kind: components.ItemWood, Count: 3, MaxStack: 20}
	// 走远离开 campfire
	eng.Send(pid, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 8, DY: 8}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	resp := eng.Request(pid, CraftRequest{UID: "u1", RecipeID: "axe"}, time.Second)
	v, _ := resp.Wait()
	r := v.(CraftResult)
	if r.Started || r.Message == "" {
		t.Fatalf("工作站缺失应拒绝, got %+v", r)
	}
}

// TestCraftInsufficient：材料不足 → 拒绝，不消耗。
func TestCraftInsufficient(t *testing.T) {
	eng, pid, _, _ := newM5World(t, testM5Cfg(t))
	createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	resp := eng.Request(pid, CraftRequest{UID: "u1", RecipeID: "axe"}, time.Second)
	v, _ := resp.Wait()
	r := v.(CraftResult)
	if r.Started {
		t.Fatalf("材料不足应拒绝, got %+v", r)
	}
}

// TestGatherDepletedNoRepeatedRespawn：采空后重复采集不 panic、不重复挂 Respawn。
func TestGatherDepletedNoRepeatedRespawn(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	bush := findWorkable(t, wa, components.ItemBerry)
	// 采 5 次（超过 work=3）：世界 actor 若 panic 会重启耗尽，syncWorld 会失败
	for i := 0; i < 5; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: player, Target: bush}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	if !wa.sim.IsAlive(bush) {
		t.Fatal("浆果丛应保留")
	}
	w := ecs.Get[components.Workable](wa.sim, bush)
	if w.WorkLeft != 0 {
		t.Fatalf("WorkLeft = %d, want 0", w.WorkLeft)
	}
}

// TestPickupTooFar：距离不够不能拾取。
func TestPickupTooFar(t *testing.T) {
	cfg := testM5Cfg(t)
	cfg.ResourcesPath = filepath.Join(t.TempDir(), "nope.json") // 无资源 seed，树手动建
	eng, pid, wa, _ := newM5World(t, cfg)
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Items[components.ItemAxe] = components.ItemStack{Kind: components.ItemAxe, Count: 1, MaxStack: 1, Durability: 10}
	tree := wa.sim.CreateEntity()
	ecs.Add(wa.sim, tree, components.Position{X: 1, Y: 0})
	ecs.Add(wa.sim, tree, components.Workable{Kind: components.ItemWood, Action: components.WorkChop, WorkLeft: 1, MaxWork: 1})

	// 装备斧头（砍/挖必须工具）→ 砍死 → 掉落
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	eng.Send(pid, Tick{})
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
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Items[components.ItemWood].Count != 0 {
		t.Fatalf("距离不够木头不应进背包, got %v", inv.Items)
	}
}
