package world

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/config"
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
		"wood": {"name":"木头","color":"#9a6b3f","stack_size":20,"drop_table":[{"kind":"wood","count":3}]},
		"flint": {"name":"燧石","color":"#9aa0a8","stack_size":20,"drop_table":[{"kind":"flint","count":2}]},
		"axe": {"name":"斧头","color":"#c9a86a","stack_size":1,"tool":{"action":"chop","efficiency":5,"durability":10}},
		"pickaxe": {"name":"镐","color":"#b8c4cf","stack_size":1,"tool":{"action":"mine","efficiency":3,"durability":10}}
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

// findWorkable 按 kind 找第一个带受激能力的实体（Choppable/Minable/Pickable）。
func findWorkable(t *testing.T, wa *WorldActor, kind components.ItemKind) ecs.Entity {
	t.Helper()
	var found ecs.Entity
	ecs.Query[interactive.Choppable](wa.sim, func(e ecs.Entity, w *interactive.Choppable) {
		if found == 0 && w.Kind == kind {
			found = e
		}
	})
	ecs.Query[interactive.Minable](wa.sim, func(e ecs.Entity, w *interactive.Minable) {
		if found == 0 && w.Kind == kind {
			found = e
		}
	})
	ecs.Query[interactive.Pickable](wa.sim, func(e ecs.Entity, w *interactive.Pickable) {
		if found == 0 && w.Kind == kind {
			found = e
		}
	})
	if found == 0 {
		t.Fatalf("找不到 kind=%d 的可作用目标", kind)
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
	for i := 0; i < 5; i++ {
		eng.Send(pid, Tick{})
	}
	// 饿 20 tick：饥饿 100 → 80
	for i := 0; i < 20; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 1 {
		t.Fatalf("采集后背包浆果 = %d, want 1", inv.CountOf(components.ItemBerry))
	}

	eng.Send(pid, Command{UID: "u1", Kind: CommandUse, Data: UseData{Player: player, Kind: components.ItemBerry}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) > 0 {
		t.Fatal("使用后背包不应再有浆果")
	}
	h := ecs.Get[components.Hunger](wa.sim, player)
	// 采集动作增加 4 tick windup：总消耗 26 tick，再由浆果恢复 8。
	if h.Level != 82 {
		t.Fatalf("使用后饥饿 = %d, want 82", h.Level)
	}
}

// TestTreeDeathDropAndPickup：装备斧头砍树（效率 5×2）→ 来源销毁 → 独立掉落 → 拾取进背包。
func TestTreeDeathDropAndPickup(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid) // 等 createPlayer 处理完，避免测试直写 sim 与 actor 并发
	// 直接给一把斧头（模板耐久 10）
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemAxe, 1, 1, 10)

	// 树（wood）在 (3,0)，玩家先走到 (2,0)
	tree := findWorkable(t, wa, components.ItemWood)
	moveTo(t, eng, pid, "u1", player, 2, 0)
	// 装备斧头
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if !ecs.Has[interactive.Chopper](wa.sim, player) {
		t.Fatal("装备斧头后玩家应有 Chopper 能力")
	}
	// 砍 2 刀（效率 5，树 WorkLeft 10）→ 归零 → Dead → 掉落
	for i := 0; i < 2; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: tree}})
		for j := 0; j < 9; j++ {
			eng.Send(pid, Tick{})
		}
	}
	syncWorld(t, eng, pid)

	if wa.sim.IsAlive(tree) {
		t.Fatal("树掉落后来源实体应销毁")
	}
	lootEntity := findLootableKind(t, wa, components.ItemWood)
	loot := ecs.Get[components.Lootable](wa.sim, lootEntity)
	if len(loot.Items) != 1 || loot.Items[0].Kind != components.ItemWood || loot.Items[0].Count != 3 {
		t.Fatalf("掉落 = %+v, want wood x3", loot.Items)
	}
	// 斧头（工具实体）耐久 10 - 2 = 8
	tool := ecs.Get[components.Equip](wa.sim, player).Item(components.SlotHand)
	if c := ecs.Get[interactive.Chopper](wa.sim, tool); c.Durability != 8 {
		t.Fatalf("斧头耐久 = %d, want 8", c.Durability)
	}

	// 拾取
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: lootEntity}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if wa.sim.IsAlive(lootEntity) {
		t.Fatal("拾取后掉落物实体应销毁")
	}
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemWood) != 3 {
		t.Fatalf("拾取后背包木头 = %d, want 3", inv.CountOf(components.ItemWood))
	}
}

// TestMineWithPickaxe：装备镐挖矿后来源销毁并生成独立燧石掉落，镐耐久 -1。
func TestMineWithPickaxe(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemPickaxe, 1, 1, 10)

	flint := findWorkable(t, wa, components.ItemFlint)
	moveTo(t, eng, pid, "u1", player, 3, 0) // 矿在 (4,0)，距离 1
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemPickaxe}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if !ecs.Has[interactive.Miner](wa.sim, player) {
		t.Fatal("装备镐后玩家应有 Miner 能力")
	}
	eng.Send(pid, Command{UID: "u1", Kind: CommandMine, Data: MineData{Player: player, Target: flint}})
	for i := 0; i < 5; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if wa.sim.IsAlive(flint) {
		t.Fatal("矿掉落后来源实体应销毁")
	}
	loot := ecs.Get[components.Lootable](wa.sim, findLootableKind(t, wa, components.ItemFlint))
	if len(loot.Items) != 1 || loot.Items[0].Kind != components.ItemFlint || loot.Items[0].Count != 2 {
		t.Fatalf("掉落 = %+v, want flint x2", loot.Items)
	}
	// 镐（工具实体）耐久 10 - 1 = 9
	tool := ecs.Get[components.Equip](wa.sim, player).Item(components.SlotHand)
	if c := ecs.Get[interactive.Miner](wa.sim, tool); c.Durability != 9 {
		t.Fatalf("镐耐久 = %d, want 9", c.Durability)
	}
}

// TestWorkActionMismatch：拿斧头挖矿 → 拒绝（工具动作不匹配），矿不消耗。
func TestWorkActionMismatch(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemAxe, 1, 1, 10)

	// 矿（flint）在 (4,0)，走到 (3,0)（距离1）
	flint := findWorkable(t, wa, components.ItemFlint)
	moveTo(t, eng, pid, "u1", player, 3, 0)
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	eng.Send(pid, Tick{})
	eng.Send(pid, Command{UID: "u1", Kind: CommandMine, Data: MineData{Player: player, Target: flint}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	w := ecs.Get[interactive.Minable](wa.sim, flint)
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
	moveTo(t, eng, pid, "u2", u2, 1, 0) // 停在 (1,0)，u1 攻击范围 2 内
	syncWorld(t, eng, pid)

	// 打 10 次 → hp 0 → Dead
	for i := 0; i < 10; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: u1, Target: u2}})
		ticks := 17
		if i == 9 {
			ticks = 9
		}
		for j := 0; j < ticks; j++ {
			eng.Send(pid, Tick{})
		}
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

// TestPlayerCorpseExcludedFromCleanup：玩家尸体不走生物尸体 TTL。
func TestPlayerCorpseExcludedFromCleanup(t *testing.T) {
	cfg := testM5Cfg(t)
	cfg.CorpseRetentionTicks = 2
	eng, pid, wa, _ := newM5World(t, cfg)
	u1 := createPlayer(t, eng, pid, "u1")
	u2 := createPlayer(t, eng, pid, "u2")
	moveTo(t, eng, pid, "u2", u2, 1, 0) // 停在 (1,0)，u1 攻击范围 2 内
	for i := 0; i < 10; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: u1, Target: u2}})
		ticks := 17
		if i == 9 {
			ticks = 9
		}
		for j := 0; j < ticks; j++ {
			eng.Send(pid, Tick{})
		}
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
	if !wa.sim.IsAlive(u2) || !ecs.Has[components.Dead](wa.sim, u2) {
		t.Fatal("玩家尸体不应被普通尸体 TTL 销毁")
	}
}

func TestCreatureCorpseCleanup(t *testing.T) {
	wa := NewWorldActor(WorldConfig{CorpseRetentionTicks: 2})
	corpse := wa.sim.CreateEntity()
	ecs.Add(wa.sim, corpse, components.Dead{Reason: "test", SinceTick: 1})
	wa.tick = 3
	wa.cleanupCorpses()
	if wa.sim.IsAlive(corpse) {
		t.Fatal("非玩家尸体超过保留时长应被销毁")
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
		for j := 0; j < 9; j++ {
			eng.Send(pid, Tick{})
		}
	}
	syncWorld(t, eng, pid)
	w := ecs.Get[interactive.Pickable](wa.sim, bush)
	if w.WorkLeft != 0 {
		t.Fatalf("采空后 WorkLeft = %d, want 0", w.WorkLeft)
	}
	r := ecs.Get[components.Respawnable](wa.sim, bush)
	if r.TicksLeft <= 0 {
		t.Fatal("采空后 Respawnable 应开始倒计时")
	}

	// 5 tick 后重生恢复
	for i := 0; i < 5; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	r = ecs.Get[components.Respawnable](wa.sim, bush)
	if r.TicksLeft != 0 {
		t.Fatal("重生后倒计时应归零")
	}
	w = ecs.Get[interactive.Pickable](wa.sim, bush)
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
		for j := 0; j < 9; j++ {
			eng.Send(pid, Tick{})
		}
	}
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 2 {
		t.Fatalf("采集后浆果 = %d, want 2", inv.CountOf(components.ItemBerry))
	}

	// 丢弃 1 个
	eng.Send(pid, Command{UID: "u1", Kind: CommandDrop,
		Data: DropData{Player: player, Kind: components.ItemBerry, Count: 1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 1 {
		t.Fatalf("丢弃后浆果 = %d, want 1", inv.CountOf(components.ItemBerry))
	}

	// 找到掉落物实体并拾取回来
	lootEntity := ecs.Entity(0)
	ecs.Query[components.Lootable](wa.sim, func(e ecs.Entity, l *components.Lootable) {
		lootEntity = e
	})
	if lootEntity == 0 {
		t.Fatal("应有掉落物实体")
	}
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: lootEntity}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 2 {
		t.Fatalf("拾取后浆果 = %d, want 2", inv.CountOf(components.ItemBerry))
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
	if inv.NonEmptyCount() != 0 {
		t.Fatalf("丢弃不足不应生效, got %v", inv.Slots)
	}
	n := 0
	ecs.Query[components.Lootable](wa.sim, func(e ecs.Entity, l *components.Lootable) { n++ })
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
	inv.Add(components.ItemWood, 3, 20, 0)
	inv.Add(components.ItemFlint, 1, 20, 0)

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
	if inv.CountOf(components.ItemWood) != 3 || inv.CountOf(components.ItemFlint) != 1 {
		t.Fatalf("tick 前不应扣材料: %v", inv.Slots)
	}
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemWood) != 0 || inv.CountOf(components.ItemFlint) != 0 {
		t.Fatalf("材料应已消耗: %v", inv.Slots)
	}
	if !ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("应有 Crafting 组件")
	}

	for i := 0; i < 4; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, player)
	axe := findStack(inv, components.ItemAxe)
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
	inv.Add(components.ItemWood, 3, 20, 0)
	// 走远离开 campfire
	moveTo(t, eng, pid, "u1", player, 8, 8)
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
	ecs.Remove[components.Respawnable](wa.sim, bush)
	// 采 5 次（超过 work=3）：世界 actor 若 panic 会重启耗尽，syncWorld 会失败
	for i := 0; i < 5; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: player, Target: bush}})
		ticks := 1
		if i < 3 {
			ticks = 9
		}
		for j := 0; j < ticks; j++ {
			eng.Send(pid, Tick{})
		}
	}
	syncWorld(t, eng, pid)
	if !wa.sim.IsAlive(bush) {
		t.Fatal("浆果丛应保留")
	}
	w := ecs.Get[interactive.Pickable](wa.sim, bush)
	if w.WorkLeft != 0 {
		t.Fatalf("WorkLeft = %d, want 0", w.WorkLeft)
	}
}

// findStack 测试助手：返回背包里某种物品的第一堆（无则零值）。
func findStack(inv *components.Inventory, kind components.ItemKind) components.ItemStack {
	for _, s := range inv.Slots {
		if s.Kind == kind && s.Count > 0 {
			return s
		}
	}
	return components.ItemStack{}
}

// TestGameConfigToProto：集中加载 + 端上契约序列化（确定性排序、字段完整）。
func TestGameConfigToProto(t *testing.T) {
	gc, err := config.LoadGameConfig(testM5Cfg(t))
	if err != nil {
		t.Fatal(err)
	}
	pc := gc.ToProto()
	if len(pc.Templates) != 5 { // berry/wood/flint/axe/pickaxe
		t.Fatalf("templates = %d, want 5", len(pc.Templates))
	}
	if len(pc.Recipes) != 2 {
		t.Fatalf("recipes = %d, want 2", len(pc.Recipes))
	}
	if len(pc.Stations) != 2 {
		t.Fatalf("stations = %d, want 2", len(pc.Stations))
	}
	if pc.InventorySlots != 20 {
		t.Fatalf("inventory_slots = %d, want 20", pc.InventorySlots)
	}
	// axe 模板带工具属性
	foundAxe := false
	for _, tc := range pc.Templates {
		if tc.Kind == components.ItemAxe {
			foundAxe = true
			if tc.Tool == nil || tc.Tool.Action != components.WorkChop || tc.Tool.Durability != 10 {
				t.Fatalf("axe tool config = %+v", tc.Tool)
			}
		}
	}
	if !foundAxe {
		t.Fatal("缺少 axe 模板配置")
	}
	// 配方带工作站（campfire）
	for _, rc := range pc.Recipes {
		if rc.Id == "axe" && rc.Workstation != components.StationCampfire {
			t.Fatalf("axe recipe workstation = %v, want campfire", rc.Workstation)
		}
	}
}

// TestAttackInterruptsCraftRefund：制作中被攻击 → Crafting 移除、材料退回、推送取消、不再产出。
func TestAttackInterruptsCraftRefund(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, testM5Cfg(t))
	u1 := createPlayer(t, eng, pid, "u1")
	u2 := createPlayer(t, eng, pid, "u2")
	syncWorld(t, eng, pid)

	inv := ecs.Get[components.Inventory](wa.sim, u1)
	inv.Add(components.ItemWood, 3, 20, 0)
	inv.Add(components.ItemFlint, 1, 20, 0)
	resp := eng.Request(pid, CraftRequest{UID: "u1", RecipeID: "axe"}, time.Second)
	v, _ := resp.Wait()
	if !v.(CraftResult).Started {
		t.Fatalf("制作未开始: %+v", v)
	}
	syncWorld(t, eng, pid)
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if !ecs.Has[components.Crafting](wa.sim, u1) {
		t.Fatal("应有 Crafting 组件")
	}
	action := ecs.Get[components.ActionState](wa.sim, u1)
	action.CommitTick += 20
	action.PhaseEndTick = action.CommitTick
	action.EndTick = action.CommitTick
	ecs.Get[components.Crafting](wa.sim, u1).TicksLeft = 20

	// u2 靠近并攻击 u1
	eng.Send(pid, Command{UID: "u2", Kind: CommandAttack, Data: AttackData{Attacker: u2, Target: u1}})
	for i := 0; i < 9; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if ecs.Has[components.Crafting](wa.sim, u1) {
		t.Fatal("受击后 Crafting 应移除")
	}
	if ecs.Has[components.ActionState](wa.sim, u1) {
		t.Fatal("受击后 ActionState 应移除")
	}
	inv = ecs.Get[components.Inventory](wa.sim, u1)
	if inv.CountOf(components.ItemWood) != 3 || inv.CountOf(components.ItemFlint) != 1 {
		t.Fatalf("材料未退回: %v", inv.Slots)
	}
	cancelled := false
	for _, ef := range pushed() {
		if ef.Route == proto.RouteCraftDone {
			if cd, ok := ef.Payload.(*proto.CraftDone); ok && !cd.Success && cd.RecipeId == "axe" {
				cancelled = true
			}
		}
	}
	if !cancelled {
		t.Fatal("未收到取消推送")
	}
	// 再 tick 若干，不应产出斧头
	for i := 0; i < 5; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	inv = ecs.Get[components.Inventory](wa.sim, u1)
	if inv.CountOf(components.ItemAxe) != 0 {
		t.Fatal("打断后不应产出斧头")
	}
}

// TestMoveInterruptsCraft：制作中走动 → 打断 + 退材料 + 取消推送。
func TestMoveInterruptsCraft(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemWood, 3, 20, 0)
	inv.Add(components.ItemFlint, 1, 20, 0)
	resp := eng.Request(pid, CraftRequest{UID: "u1", RecipeID: "axe"}, time.Second)
	v, _ := resp.Wait()
	if !v.(CraftResult).Started {
		t.Fatalf("制作未开始: %+v", v)
	}
	syncWorld(t, eng, pid)
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	eng.Send(pid, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 3, DY: 0}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("走动后 Crafting 应移除")
	}
	if ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("走动后 ActionState 应移除")
	}
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemWood) != 3 || inv.CountOf(components.ItemFlint) != 1 {
		t.Fatalf("材料未退回: %v", inv.Slots)
	}
	cancelled := false
	for _, ef := range pushed() {
		if ef.Route == proto.RouteCraftDone {
			if cd, ok := ef.Payload.(*proto.CraftDone); ok && !cd.Success {
				cancelled = true
			}
		}
	}
	if !cancelled {
		t.Fatal("未收到取消推送")
	}
}

// TestCancelCraftCommand：主动取消命令 → Crafting 移除 + 材料退回。
func TestCancelCraftCommand(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemWood, 3, 20, 0)
	inv.Add(components.ItemFlint, 1, 20, 0)
	resp := eng.Request(pid, CraftRequest{UID: "u1", RecipeID: "axe"}, time.Second)
	v, _ := resp.Wait()
	if !v.(CraftResult).Started {
		t.Fatalf("制作未开始: %+v", v)
	}
	syncWorld(t, eng, pid)
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	eng.Send(pid, Command{UID: "u1", Kind: CommandCancelCraft, Data: CancelCraftData{Player: player}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("取消后 Crafting 应移除")
	}
	if ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("取消后 ActionState 应移除")
	}
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemWood) != 3 || inv.CountOf(components.ItemFlint) != 1 {
		t.Fatalf("材料未退回: %v", inv.Slots)
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
	inv.Add(components.ItemAxe, 1, 1, 10)
	tree := wa.sim.CreateEntity()
	ecs.Add(wa.sim, tree, components.Position{X: 1, Y: 0})
	ecs.Add(wa.sim, tree, interactive.Choppable{Kind: components.ItemWood, WorkLeft: 1, MaxWork: 1})
	ecs.Add(wa.sim, tree, components.DropSource{Category: components.DropSourceResource, ResourceKind: components.ItemWood})

	// 装备斧头（砍/挖必须工具）→ 砍死 → 掉落
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	eng.Send(pid, Tick{})
	eng.Send(pid, Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: tree}})
	for i := 0; i < 5; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	lootEntity := findLootableKind(t, wa, components.ItemWood)

	// 走远再拾取 → 距离不够
	moveTo(t, eng, pid, "u1", player, 8, 0)
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: lootEntity}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if !wa.sim.IsAlive(lootEntity) {
		t.Fatal("距离不够不应拾取成功")
	}
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemWood) != 0 {
		t.Fatalf("距离不够木头不应进背包, got %v", inv.Slots)
	}
}

// TestSplit：从槽 0 拆 1 个到第一个空槽。
func TestSplit(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemWood, 2, 20, 0)

	eng.Send(pid, Command{UID: "u1", Kind: CommandSplit, Data: SplitData{Player: player, FromSlot: 0, Count: 1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Slot(0).Kind != components.ItemWood || inv.Slot(0).Count != 1 {
		t.Fatalf("槽0 = %+v, want wood x1", inv.Slot(0))
	}
	if inv.Slot(1).Kind != components.ItemWood || inv.Slot(1).Count != 1 {
		t.Fatalf("槽1 = %+v, want wood x1", inv.Slot(1))
	}
	if inv.CountOf(components.ItemWood) != 2 {
		t.Fatalf("木头总量 = %d, want 2", inv.CountOf(components.ItemWood))
	}
}

// TestSplitInvalid：数量超/空槽拆分 → 不生效。
func TestSplitInvalid(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemWood, 2, 20, 0)

	eng.Send(pid, Command{UID: "u1", Kind: CommandSplit, Data: SplitData{Player: player, FromSlot: 0, Count: 99}})
	eng.Send(pid, Tick{})
	eng.Send(pid, Command{UID: "u1", Kind: CommandSplit, Data: SplitData{Player: player, FromSlot: 5, Count: 1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.Slot(0).Count != 2 || inv.NonEmptyCount() != 1 {
		t.Fatalf("非法拆分不应生效: slot0=%+v nonempty=%d", inv.Slot(0), inv.NonEmptyCount())
	}
}

// TestSlotStackAndCapacity：跨槽堆叠（25→20+5）+ 20 槽容量 + 满包拒收。
func TestSlotStackAndCapacity(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.NonEmptyCount() != 0 || len(inv.Slots) != 20 {
		t.Fatalf("初始应为 20 空槽, got nonempty=%d slots=%d", inv.NonEmptyCount(), len(inv.Slots))
	}
	if added := inv.Add(components.ItemWood, 25, 20, 0); added != 25 || inv.CountOf(components.ItemWood) != 25 || inv.NonEmptyCount() != 2 {
		t.Fatalf("25 木头: added=%d total=%d nonempty=%d", added, inv.CountOf(components.ItemWood), inv.NonEmptyCount())
	}
	for k := 10; k < 28; k++ {
		inv.Add(components.ItemKind(k), 1, 20, 0)
	}
	if inv.NonEmptyCount() != 20 {
		t.Fatalf("应满 20 槽, got %d", inv.NonEmptyCount())
	}
	if got := inv.Add(components.ItemFlint, 1, 20, 0); got != 0 {
		t.Fatalf("满包应拒收, got %d", got)
	}
}

// TestEquipUnequipToolRoundTrip：装备斧头 → 获得 Chopper + 工具耐久；
// 卸下 → 斧头（含当前耐久）回背包 + 失去 Chopper；徒手砍树不生效。
func TestEquipUnequipToolRoundTrip(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemAxe, 1, 1, 10)

	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemAxe}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if !ecs.Has[interactive.Chopper](wa.sim, player) {
		t.Fatal("装备斧头后应有 Chopper 能力")
	}
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemAxe) != 0 {
		t.Fatal("装备后背包不应再有斧头")
	}

	// 砍树一次：树 WorkLeft 10 → 5，工具耐久 10 → 9
	tree := findWorkable(t, wa, components.ItemWood)
	moveTo(t, eng, pid, "u1", player, 2, 0)
	eng.Send(pid, Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: tree}})
	for i := 0; i < 5; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	// 卸下 → 斧头（耐久 9）回背包，玩家失去 Chopper
	eng.Send(pid, Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: 0}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if ecs.Has[interactive.Chopper](wa.sim, player) {
		t.Fatal("卸下后不应再有 Chopper")
	}
	inv = ecs.Get[components.Inventory](wa.sim, player)
	if findStack(inv, components.ItemAxe).Durability != 9 {
		t.Fatalf("卸下后斧头耐久 = %d, want 9", findStack(inv, components.ItemAxe).Durability)
	}

	// 徒手砍树不生效（chop 必须工具）
	eng.Send(pid, Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: tree}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	w := ecs.Get[interactive.Choppable](wa.sim, tree)
	if w.WorkLeft != 5 {
		t.Fatalf("徒手砍树不应生效：WorkLeft = %d, want 5", w.WorkLeft)
	}
}
