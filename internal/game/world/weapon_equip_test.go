package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// 武器物品 → 攻击力的接线测试。
//
// 设计（见 config.WeaponSpec 与 command_handler.equipWeapon）：
//   - 武器是**手持槽**装备（与工具同槽），攻击能力挂在武器实体上（自描述），
//     同时**复制到玩家**（快照里就是有效值，客户端显示不会骗人）；
//   - 攻击判定统一走 interactive.ActorCap[Attacker]（手持优先于自身），
//     所以攻击代码里没有任何"有没有武器"的分支；
//   - 与工具最大的不同：空手**也有**攻击力（baseAttacker），所以卸下武器是
//     "恢复空手"而不是"移除能力" —— 这一条必须由测试守住，否则玩家会永久保留武器伤害。

// newWeaponWorld 带真实模板表的世界（武器数值来自 resource_templates.json 的 weapon 段）。
func newWeaponWorld(t *testing.T) *WorldActor {
	t.Helper()
	return NewWorldActor(WorldConfig{
		TemplatesPath: "../../../configs/resource_templates.json",
		AttackDamage:  10, // 空手基准：卸下武器后必须回到 10
	})
}

// attackOnce 让玩家对目标打一次（完整命令路径 + 动作 commit 结算）。
//
// 跑满 20 tick（起手 8 + 收招 8，外加余量）：只跑到 commit 的话动作还在 recovery，
// 紧接着的第二次攻击会被"动作忙"拒掉，测试会误判成"伤害没生效"。
func attackOnce(wa *WorldActor, uid string, player, target ecs.Entity) {
	wa.cmds.Handle(Command{UID: uid, Kind: CommandAttack, Data: AttackData{Attacker: player, Target: target}})
	runActionTicks(wa, 20)
}

// handSlot 手持槽实体（没有 Equip 组件时返回 0 —— 空手时该组件可能根本不存在）。
func handSlot(wa *WorldActor, player ecs.Entity) ecs.Entity {
	if !ecs.Has[components.Equip](wa.sim, player) {
		return 0
	}
	return ecs.Get[components.Equip](wa.sim, player).Item(components.SlotHand)
}

// 装备长矛：玩家自身组件与 ActorCap 都变成武器值，实打按武器伤害结算，卸下后完全恢复。
func TestWeaponRaisesAttackDamageAndRestoresOnUnequip(t *testing.T) {
	wa := newWeaponWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	giveItem(wa, player, components.ItemSpear, 1)

	// 空手基准：ActorCap 解析到自身能力（10 伤）
	if _, a := interactive.ActorCap[interactive.Attacker](wa.sim, player); a == nil || a.AttackDamage != 10 {
		t.Fatalf("空手攻击力应为 10，实际 %+v", a)
	}

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip,
		Data: EquipData{Player: player, Kind: components.ItemSpear}})

	// ① 玩家自身组件被覆盖（快照发给客户端的就是这个值）
	own := ecs.Get[interactive.Attacker](wa.sim, player)
	if own.AttackDamage != 22 {
		t.Fatalf("装备后玩家的攻击力应为武器的 22，实际 %d —— "+
			"只挂在武器实体上的话，客户端显示会一直是空手值", own.AttackDamage)
	}
	// ② 攻击判定走的 ActorCap 也解析到武器
	if _, eff := interactive.ActorCap[interactive.Attacker](wa.sim, player); eff == nil || eff.AttackDamage != 22 {
		t.Fatalf("装备后 ActorCap 应解析到武器攻击力 22，实际 %+v", eff)
	}
	// ③ 武器实体自描述（能力挂在它身上，卸下时无需反推）
	hand := handSlot(wa, player)
	if hand == 0 || !ecs.Has[interactive.Attacker](wa.sim, hand) {
		t.Fatal("手持槽应挂着一个带攻击能力的武器实体")
	}
	if inv := ecs.Get[components.Inventory](wa.sim, player); inv.CountOf(components.ItemSpear) != 0 {
		t.Fatalf("装备后长矛应从背包移出，实际还剩 %d 件", inv.CountOf(components.ItemSpear))
	}

	// ④ 实打一下：100 - 22 = 78
	target := wa.sim.CreateEntity()
	ecs.Add(wa.sim, target, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, target, components.Health{Cur: 100, Max: 100})
	ecs.Add(wa.sim, target, components.Attackable{})
	attackOnce(wa, "u1", player, target)
	if hp := ecs.Get[components.Health](wa.sim, target).Cur; hp != 78 {
		t.Fatalf("持矛攻击应造成 22 伤害（100 → 78），实际 %d —— 武器只是显示、没接进攻击判定", hp)
	}

	// ⑤ 卸下：必须恢复空手 10（不能把武器伤害留在身上），且武器回到背包
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip, Data: EquipData{Player: player}})
	if own := ecs.Get[interactive.Attacker](wa.sim, player); own.AttackDamage != 10 || own.AttackRange != 2 {
		t.Fatalf("卸下武器后应恢复空手能力（10 伤/2 距离），实际 %+v", own)
	}
	if inv := ecs.Get[components.Inventory](wa.sim, player); inv.CountOf(components.ItemSpear) != 1 {
		t.Fatalf("卸下后长矛应回到背包，实际 %d 件", inv.CountOf(components.ItemSpear))
	}

	// ⑥ 再打一下：回到 10 伤（78 - 10 = 68）
	attackOnce(wa, "u1", player, target)
	if hp := ecs.Get[components.Health](wa.sim, target).Cur; hp != 68 {
		t.Fatalf("卸下武器后攻击应回到 10 伤害（78 → 68），实际 %d", hp)
	}
}

// 武器与工具共用**同一个手持槽**：换成工具后武器伤害不能"粘"在身上。
//
// 这是最容易漏的一条：两条装备路径（工具/武器）各写一遍时，
// 很容易只在"卸下武器"的入口恢复空手，而"装工具顶掉武器"这条路径漏掉。
func TestSwitchingFromWeaponToToolRestoresBareHand(t *testing.T) {
	wa := newWeaponWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	giveItem(wa, player, components.ItemSpear, 1)
	giveItem(wa, player, components.ItemAxe, 1)

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip,
		Data: EquipData{Player: player, Kind: components.ItemSpear}})
	if own := ecs.Get[interactive.Attacker](wa.sim, player); own.AttackDamage != 22 {
		t.Fatalf("先装长矛应把攻击力提到 22，实际 %d", own.AttackDamage)
	}

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip,
		Data: EquipData{Player: player, Kind: components.ItemAxe}})
	if own := ecs.Get[interactive.Attacker](wa.sim, player); own.AttackDamage != 10 {
		t.Fatalf("换成斧头后攻击力应回到空手 10（工具不提供攻击力），实际 %d", own.AttackDamage)
	}
	if inv := ecs.Get[components.Inventory](wa.sim, player); inv.CountOf(components.ItemSpear) != 1 {
		t.Fatalf("被顶掉的长矛应放回背包，实际 %d 件", inv.CountOf(components.ItemSpear))
	}
	// 工具能力仍然生效（换装不能把工具能力也弄丢）
	if _, c := interactive.ActorCap[interactive.Chopper](wa.sim, player); c == nil {
		t.Fatal("换成斧头后应保有砍伐能力")
	}
}

// 背包里没有武器时不能装备：既不产生武器实体，也不改攻击力。
func TestEquipWeaponWithoutItemIsRejected(t *testing.T) {
	wa := newWeaponWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})

	handBefore := handSlot(wa, player)
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandEquip,
		Data: EquipData{Player: player, Kind: components.ItemSpear}})

	if own := ecs.Get[interactive.Attacker](wa.sim, player); own.AttackDamage != 10 {
		t.Fatalf("背包里没有长矛时不该改变攻击力，实际 %d", own.AttackDamage)
	}
	if hand := handSlot(wa, player); hand != handBefore {
		t.Fatalf("背包里没有长矛时不该占用手持槽：%d → %d", handBefore, hand)
	}
}
