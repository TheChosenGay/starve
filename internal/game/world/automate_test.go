package world

import (
	"encoding/json"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/systems"
	"starve/internal/game/world/behavior"
	"starve/pkg/proto"
)

// addTree 摆一棵可砍伐的树（Choppable）。
func addTree(t *testing.T, wa *WorldActor, x, y, work int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, interactive.Choppable{Kind: components.ItemWood, WorkLeft: work, MaxWork: work})
	return e
}

// equipTestAxe 直接给实体挂砍伐能力（测试免走背包/装备流程）。
func equipTestAxe(wa *WorldActor, e ecs.Entity) {
	ecs.Add(wa.sim, e, interactive.Chopper{Efficiency: 1, Range: 2, Durability: -1})
}

// 空格自动行为：更近的花（Picker，距离 1）优先于更远的树（Chopper，距离 2）。
func TestAutomatePickCloserOverChop(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	equipTestAxe(wa, player)
	tree := addTree(t, wa, 0, 2, 3)
	flower := addBush(t, wa, 0, 1, 2)

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	runActionTicks(wa, 5)

	if got := ecs.Get[interactive.Choppable](wa.sim, tree).WorkLeft; got != 3 {
		t.Fatalf("树工作量 = %d, want 3（应选更近的花）", got)
	}
	if got := ecs.Get[interactive.Pickable](wa.sim, flower).WorkLeft; got != 1 {
		t.Fatalf("花工作量 = %d, want 1（采集一次）", got)
	}
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 1 {
		t.Fatalf("背包 berry = %d, want 1", inv.CountOf(components.ItemBerry))
	}
}

// 空格自动行为：只有树在可作用距离内 → 执行砍伐；超出 Picker 范围的花不干扰。
func TestAutomateChopWhenNoCloserPickable(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 5, Y: 5})
	equipTestAxe(wa, player)
	tree := addTree(t, wa, 5, 7, 3)
	flower := addBush(t, wa, 5, 8, 2) // 距离 3：超出 Picker.Range=1，不可选

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	runActionTicks(wa, 5)

	if got := ecs.Get[interactive.Choppable](wa.sim, tree).WorkLeft; got != 2 {
		t.Fatalf("树工作量 = %d, want 2（砍一次）", got)
	}
	if got := ecs.Get[interactive.Pickable](wa.sim, flower).WorkLeft; got != 2 {
		t.Fatalf("远处花不应被采集, WorkLeft = %d, want 2", got)
	}
}

// FindBest 跳过已耗尽（WorkLeft=0）的目标，选更远的可用目标。
func TestAutomateSkipsDepletedTarget(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	equipTestAxe(wa, player)
	depleted := addTree(t, wa, 0, 1, 0)
	live := addTree(t, wa, 0, 2, 3)

	intent, target, ok := behavior.FindBest(wa.sim, player, 8)
	if !ok {
		t.Fatal("应找到可砍伐的树")
	}
	if intent != interactive.IntentChop || target != live {
		t.Fatalf("intent=%v target=%d, want chop 目标 %d（跳过已耗尽的 %d）", intent, target, live, depleted)
	}
}

// 无匹配能力（徒手 + 只有树）→ FindBest 返回未找到。
func TestAutomateNoMatch(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	tree := addTree(t, wa, 0, 1, 3) // 徒手没有 Chopper，砍不了

	if intent, target, ok := behavior.FindBest(wa.sim, player, 8); ok {
		t.Fatalf("徒手+树不应匹配到行为, intent=%v target=%d", intent, target)
	}
	// 命令路径同样无副作用
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	if got := ecs.Get[interactive.Choppable](wa.sim, tree).WorkLeft; got != 3 {
		t.Fatalf("树工作量不应变化, got %d", got)
	}
}

// 自动行为搜索半径受 AOI 限制：目标超出感知半径不可选。
func TestAutomateRespectsAOIRadius(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	ecs.Set(wa.sim, player, components.AOI{Radius: 1})
	tree := addTree(t, wa, 0, 2, 3) // 距离 2 > AOI 1，且 Chopper 范围 2 也被 AOI 截断
	equipTestAxe(wa, player)

	if intent, target, ok := behavior.FindBest(wa.sim, player, 1); ok {
		t.Fatalf("AOI=1 不应找到距离 2 的树, intent=%v target=%d", intent, target)
	}
	if got := ecs.Get[interactive.Choppable](wa.sim, tree).WorkLeft; got != 3 {
		t.Fatalf("树工作量不应变化, got %d", got)
	}
}

// 自动行为攻击：徒手 Attacker（默认范围 2）选最近可攻击目标并结算伤害。
func TestAutomateAttack(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AttackDamage: 10})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	target := wa.sim.CreateEntity()
	ecs.Add(wa.sim, target, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, target, components.Health{Cur: 30, Max: 30})
	ecs.Add(wa.sim, target, components.Attackable{})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	runActionTicks(wa, 9)

	if hp := ecs.Get[components.Health](wa.sim, target); hp.Cur != 20 {
		t.Fatalf("目标 HP = %d, want 20（10 伤害）", hp.Cur)
	}
}

func TestAttackOnlyIgnoresCloserNonAttackTargets(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AttackDamage: 10})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	flower := addBush(t, wa, 0, 1, 2)
	loot := addLoot(t, wa, 1, 0, components.ItemWood, 1)
	target := addActionTarget(wa, 0, 2, 30)

	wa.cmds.Handle(Command{
		UID: "u1", Kind: CommandAutomate,
		Data: AutomateData{Player: player, Mode: proto.AutomateMode_AUTOMATE_MODE_ATTACK_ONLY},
	})
	runActionTicks(wa, 9)

	if hp := ecs.Get[components.Health](wa.sim, target).Cur; hp != 20 {
		t.Fatalf("ATTACK_ONLY 目标 hp=%d, want 20", hp)
	}
	if got := ecs.Get[interactive.Pickable](wa.sim, flower).WorkLeft; got != 2 {
		t.Fatalf("ATTACK_ONLY 不应采集，WorkLeft=%d", got)
	}
	if !wa.sim.IsAlive(loot) {
		t.Fatal("ATTACK_ONLY 不应拾取更近掉落物")
	}
}

func TestAttackOnlyNearestTieUsesEntityID(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	first := addActionTarget(wa, 1, 0, 30)
	addActionTarget(wa, 0, 1, 30)

	intent, target, ok := behavior.FindBestForIntent(
		wa.sim, player, 8, interactive.IntentAttack,
	)
	if !ok || intent != interactive.IntentAttack || target != first {
		t.Fatalf("同距选择=(%v,%d,%v), want attack/%d/true", intent, target, ok, first)
	}
}

func TestAttackOnlySkipsUnavailableAndOutsideAOI(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	ecs.Set(wa.sim, player, components.AOI{Radius: 3})

	depleted := addActionTarget(wa, 0, 1, 0)
	dead := addActionTarget(wa, 1, 0, 30)
	ecs.Add(wa.sim, dead, components.Dead{Reason: "test"})
	offline := addActionTarget(wa, -1, 0, 30)
	ecs.Add(wa.sim, offline, components.Offline{})
	outside := addActionTarget(wa, 0, 4, 30)
	live := addActionTarget(wa, 0, 2, 30)

	_, target, ok := behavior.FindBestForIntent(
		wa.sim, player, 3, interactive.IntentAttack,
	)
	if !ok || target != live {
		t.Fatalf(
			"可用攻击目标=%d/%v, want %d；跳过 depleted=%d dead=%d offline=%d outside=%d",
			target, ok, live, depleted, dead, offline, outside,
		)
	}
}

func TestAttackOnlyBusyDoesNotQueueDuplicateActions(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	addActionTarget(wa, 0, 1, 30)
	mode := proto.AutomateMode_AUTOMATE_MODE_ATTACK_ONLY
	command := Command{
		UID: "u1", Kind: CommandAutomate,
		Data: AutomateData{Player: player, Mode: mode},
	}

	wa.cmds.Handle(command)
	wa.cmds.Handle(command)
	if got := len(ecs.Resource[systems.ControlQueue](wa.sim).Intents); got != 2 {
		t.Fatalf("待仲裁攻击数=%d, want 2（Control 负责 last-wins）", got)
	}
	tickWorld(wa)
	if !ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("首个 ATTACK_ONLY 应启动 ActionState")
	}
	results := ecs.Resource[systems.ControlQueue](wa.sim).Results
	if len(results) != 2 || !results[0].Superseded || !results[1].Accepted {
		t.Fatalf("自动攻击归并结果=%+v", results)
	}
	wa.cmds.Handle(command)
	if got := len(ecs.Resource[systems.ControlQueue](wa.sim).Intents); got != 0 {
		t.Fatalf("busy 时不应再排动作，got %d", got)
	}
}

func TestAutomateJournalLegacyDefaultsToAny(t *testing.T) {
	entry := JournalEntry{
		Kind: CommandAutomate,
		Data: json.RawMessage(`{"Player":42}`),
	}
	data, ok := entry.decodeData().(AutomateData)
	if !ok || data.Player != 42 || data.Mode != proto.AutomateMode_AUTOMATE_MODE_ANY {
		t.Fatalf("旧 automate journal 解码=%+v, ok=%v", data, ok)
	}
}
