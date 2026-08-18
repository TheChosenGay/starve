package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// newArmorWorld 带护甲模板的世界（测试装备路径需要模板里的 armor 属性）。
func newArmorWorld(t *testing.T) *WorldActor {
	t.Helper()
	return NewWorldActor(WorldConfig{TemplatesPath: "../../../configs/resource_templates.json"})
}

// giveItem 直接给玩家背包塞物品（测试免走采集/制作）。
func giveItem(wa *WorldActor, player ecs.Entity, kind components.ItemKind, count int) {
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(kind, count, 1, 0)
}

// equippedPlayer 造一个穿好指定护甲的玩家（按顺序逐个装备），返回（世界, 玩家）。
func equippedPlayer(t *testing.T, kinds []components.ItemKind) (*WorldActor, ecs.Entity) {
	t.Helper()
	wa := newArmorWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	for _, k := range kinds {
		giveItem(wa, player, k, 1)
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: k}})
	}
	return wa, player
}

// dealDamageTo 用一个指定伤害的攻击者打玩家一次（走 AttackBehavior 结算）。
func dealDamageTo(t *testing.T, wa *WorldActor, player ecs.Entity, damage int) {
	t.Helper()
	attacker := wa.sim.CreateEntity()
	ecs.Add(wa.sim, attacker, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, attacker, interactive.Attacker{AttackDamage: damage, AttackRange: 1})
	if !interactive.Do(wa.sim, attacker, player, interactive.IntentAttack) {
		t.Fatal("攻击应执行")
	}
}

// 装备木甲：身穿槽位挂上护甲实体，背包扣一件；防御只存在护甲实体上，穿戴者不存 Defense。
func TestEquipWoodArmor(t *testing.T) {
	wa := newArmorWorld(t)
	player := wa.createPlayer("u1")
	giveItem(wa, player, components.ItemWoodArmor, 1)

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	eq := ecs.Get[components.Equip](wa.sim, player)
	if eq.Body == 0 {
		t.Fatal("木甲应挂到身穿槽位")
	}
	if ecs.Has[components.Defense](wa.sim, player) {
		t.Fatal("穿戴者身上不应有 Defense（防御只留在护甲实体）")
	}
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWoodArmor); got != 0 {
		t.Fatalf("背包木甲应扣一件, got %d", got)
	}
}

// 头盔（head 20%）+ 木甲（body 40%）叠加：受击时从 Equip 槽位求和 → 10 伤害只掉 4。
func TestEquipArmorStacking(t *testing.T) {
	wa, player := equippedPlayer(t, []components.ItemKind{components.ItemHelmet, components.ItemWoodArmor})
	dealDamageTo(t, wa, player, 10)
	if hp := ecs.Get[components.Health](wa.sim, player); hp.Cur != 96 {
		t.Fatalf("头+身 60%% 防御下 10 伤害应扣 4, Cur = %d, want 96", hp.Cur)
	}
}

// 卸下全部（kind=0）：槽位清空、护甲回背包。
func TestUnequipAllReturnsArmor(t *testing.T) {
	wa := newArmorWorld(t)
	player := wa.createPlayer("u1")
	giveItem(wa, player, components.ItemWoodArmor, 1)
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: 0}})

	eq := ecs.Get[components.Equip](wa.sim, player)
	if eq.Head != 0 || eq.Hand != 0 || eq.Body != 0 {
		t.Fatal("卸下全部后槽位应清空")
	}
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWoodArmor); got != 1 {
		t.Fatalf("木甲应放回背包, got %d", got)
	}
}

// 同槽位换装：旧护甲放回背包，新护甲生效（受击按新装备结算）。
func TestEquipArmorReplacesSameSlot(t *testing.T) {
	wa := newArmorWorld(t)
	player := wa.createPlayer("u1")
	giveItem(wa, player, components.ItemWoodArmor, 2)
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWoodArmor); got != 1 {
		t.Fatalf("旧木甲应放回背包, got %d", got)
	}
	dealDamageTo(t, wa, player, 10)
	if hp := ecs.Get[components.Health](wa.sim, player); hp.Cur != 94 {
		t.Fatalf("换装后 40%% 防御下 10 伤害应扣 6, Cur = %d, want 94", hp.Cur)
	}
}

// 护甲减免生效：Attackable 受击时从 Equip 槽位读 Defense，10 伤害 → 6。
func TestArmorMitigatesDamage(t *testing.T) {
	wa, player := equippedPlayer(t, []components.ItemKind{components.ItemWoodArmor})
	dealDamageTo(t, wa, player, 10)
	if hp := ecs.Get[components.Health](wa.sim, player); hp.Cur != 94 {
		t.Fatalf("40%% 防御下 10 伤害应扣 6, Cur = %d, want 94", hp.Cur)
	}
}
