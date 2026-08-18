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

// 装备木甲：身穿槽位挂上护甲实体，防御复制到玩家，背包扣一件。
func TestEquipWoodArmor(t *testing.T) {
	wa := newArmorWorld(t)
	player := wa.createPlayer("u1")
	giveItem(wa, player, components.ItemWoodArmor, 1)

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	eq := ecs.Get[interactive.Equip](wa.sim, player)
	if eq.Body == 0 {
		t.Fatal("木甲应挂到身穿槽位")
	}
	if d := ecs.Get[components.Defense](wa.sim, player); d.Percent != 40 {
		t.Fatalf("玩家防御 = %d, want 40", d.Percent)
	}
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWoodArmor); got != 0 {
		t.Fatalf("背包木甲应扣一件, got %d", got)
	}
}

// 头盔（head 20%）+ 木甲（body 40%）叠加 → 防御 60%。
func TestEquipArmorStacking(t *testing.T) {
	wa := newArmorWorld(t)
	player := wa.createPlayer("u1")
	giveItem(wa, player, components.ItemHelmet, 1)
	giveItem(wa, player, components.ItemWoodArmor, 1)

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemHelmet}})
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	if d := ecs.Get[components.Defense](wa.sim, player); d.Percent != 60 {
		t.Fatalf("头+身防御 = %d, want 60", d.Percent)
	}
}

// 卸下全部（kind=0）：槽位清空、防御移除、护甲回背包。
func TestUnequipAllReturnsArmor(t *testing.T) {
	wa := newArmorWorld(t)
	player := wa.createPlayer("u1")
	giveItem(wa, player, components.ItemWoodArmor, 1)
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: 0}})

	eq := ecs.Get[interactive.Equip](wa.sim, player)
	if eq.Head != 0 || eq.Hand != 0 || eq.Body != 0 {
		t.Fatal("卸下全部后槽位应清空")
	}
	if ecs.Has[components.Defense](wa.sim, player) {
		t.Fatal("卸下护甲后玩家防御应移除")
	}
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWoodArmor); got != 1 {
		t.Fatalf("木甲应放回背包, got %d", got)
	}
}

// 同槽位换装：旧护甲放回背包，防御取新装备值。
func TestEquipArmorReplacesSameSlot(t *testing.T) {
	wa := newArmorWorld(t)
	player := wa.createPlayer("u1")
	giveItem(wa, player, components.ItemWoodArmor, 2)
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player, Kind: components.ItemWoodArmor}})

	if d := ecs.Get[components.Defense](wa.sim, player); d.Percent != 40 {
		t.Fatalf("换装后防御 = %d, want 40", d.Percent)
	}
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWoodArmor); got != 1 {
		t.Fatalf("旧木甲应放回背包, got %d", got)
	}
}

// 护甲减免生效：攻击者打带 40% 防御的玩家，10 伤害 → 6。
func TestArmorMitigatesDamage(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	ecs.Add(wa.sim, player, components.Defense{Percent: 40})
	attacker := wa.sim.CreateEntity()
	ecs.Add(wa.sim, attacker, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, attacker, interactive.Attacker{AttackDamage: 10, AttackRange: 1})

	if !interactive.Do(wa.sim, attacker, player, interactive.IntentAttack) {
		t.Fatal("攻击应执行")
	}
	if hp := ecs.Get[components.Health](wa.sim, player); hp.Cur != 94 {
		t.Fatalf("40%% 防御下 10 伤害应扣 6, Cur = %d, want 94", hp.Cur)
	}
}
