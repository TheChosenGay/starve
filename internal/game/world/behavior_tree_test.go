package world

import (
	"testing"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/behavior"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	game "starve/pkg/proto/game"
)

// 本文件验证行为树**真的接在 AISystem 上**（而非被 legacy 回退路径掩盖）。
//
// 背景：AISystem 在实体没有 BehaviorTree 组件时会回退到旧状态机（兼容旧存档）。
// 因此"生物测试全绿"并不能证明行为树是对的——必须显式挂树并断言它的行为。

// addTreeCreature 摆一只走行为树的生物。
func addTreeCreature(t *testing.T, wa *WorldActor, x, y int, canAttack bool, hostilePlayers bool) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: 30, Max: 30})
	ecs.Add(wa.sim, e, components.Attackable{})
	ecs.Add(wa.sim, e, components.Moveable{Speed: 10})
	ecs.Add(wa.sim, e, components.AOI{Radius: 6})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{},
		HomeX: x, HomeY: y, RoamRadius: 4,
	})
	ecs.Add(wa.sim, e, components.AI{
		State: components.CreatureIdle, FleeHP: 8, HitMemoryTicks: 10,
		HostilePlayers: hostilePlayers,
	})
	addBehaviorTree(wa, e, canAttack)
	dmg, rng, cd := 0, 1, 5
	if canAttack {
		dmg, rng, cd = 8, 1, 5
	}
	ecs.Add(wa.sim, e, interactive.Attacker{AttackDamage: dmg, AttackRange: rng, AttackCooldown: cd})
	return e
}

// 行为树路径：生物应锁定玩家并从 idle 进入 attack（与 legacy 语义一致）。
func TestBehaviorTreeLockAndAttack(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wolf := addTreeCreature(t, wa, 0, 0, true, true)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	hp := ecs.Get[components.Health](wa.sim, player)

	tickWorld(wa)
	ai := ecs.Get[components.AI](wa.sim, wolf)
	if ai.Target != player {
		t.Fatalf("行为树应锁定玩家: target=%d want %d", ai.Target, player)
	}
	if ai.State != components.CreatureAttack {
		t.Fatalf("同格应在攻击态: state=%v", ai.State)
	}
	// 动作时间轴推进后应真的扣血（证明树提交的控制意图被 ControlSystem 接纳）
	runActionTicks(wa, 8)
	if hp.Cur != 92 {
		t.Fatalf("行为树驱动的攻击应扣 8 血: hp=%d", hp.Cur)
	}
}

// 行为树路径：被动生物（无攻击力）看到敌对目标应逃跑，而不是攻击。
func TestBehaviorTreePreyFlees(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	prey := addTreeCreature(t, wa, 0, 0, false, true) // canAttack=false → PreyTree
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})

	tickWorld(wa)
	ai := ecs.Get[components.AI](wa.sim, prey)
	if ai.Target != player {
		t.Fatalf("被动生物应锁定威胁: target=%d", ai.Target)
	}
	if ai.State != components.CreatureFlee {
		t.Fatalf("被动生物有威胁应逃跑: state=%v", ai.State)
	}
}

// 低血逃跑：掠食者血量低于 FleeHP 时应从攻击切到 flee。
func TestBehaviorTreeLowHPFlees(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wolf := addTreeCreature(t, wa, 0, 0, true, true)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})

	tickWorld(wa)
	if ai := ecs.Get[components.AI](wa.sim, wolf); ai.State != components.CreatureAttack {
		t.Fatalf("前置条件：应先在攻击态，实际 %v", ai.State)
	}
	// 打到阈值以下
	hp := ecs.Get[components.Health](wa.sim, wolf)
	hp.Cur = 5 // FleeHP = 8
	tickWorld(wa)
	if ai := ecs.Get[components.AI](wa.sim, wolf); ai.State != components.CreatureFlee {
		t.Fatalf("低血应逃跑: state=%v hp=%d fleeHP=%d", ai.State, hp.Cur, ai.FleeHP)
	}
}

// 攻击冷却：行为树必须维护 AI.Cooldown 倒计时，否则打完一次就永远不再攻击。
//
// 这是行为树重构中真实出现过的回归——原实现的递减藏在 attack() 里，
// 换成行为树后那条路径不再被调用，导致生物只攻击一次。
func TestBehaviorTreeAttackCooldownCountsDown(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wolf := addTreeCreature(t, wa, 0, 0, true, true)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	hp := ecs.Get[components.Health](wa.sim, player)

	tickWorld(wa)
	// 冷却在**接纳攻击那一次 tick** 就被置为 AttackCooldown（见 ControlSystem），
	// 随后由 AISystem 每 tick 递减。这里刚好在接纳后立刻检查，应 > 0。
	if cd := ecs.Get[components.AI](wa.sim, wolf).Cooldown; cd <= 0 {
		t.Fatalf("接纳攻击后应进入冷却: cooldown=%d", cd)
	}
	runActionTicks(wa, 8) // 第一刀
	first := hp.Cur
	if first != 92 {
		t.Fatalf("第一刀应扣 8 血: hp=%d", first)
	}
	// 冷却结束后必须能再砍一刀
	for i := 0; i < 40; i++ {
		tickWorld(wa)
	}
	if hp.Cur >= first {
		t.Fatalf("冷却结束后应继续攻击: hp=%d（第一刀后 %d）", hp.Cur, first)
	}
}

// 运行态（Running 游标 + 冷却计数器）必须随快照编码，且能无损解码。
//
// 否则读档后 AI 会从树根重新决策，行为突变。
func TestBehaviorTreeSnapshotCodec(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wolf := addTreeCreature(t, wa, 0, 0, true, true)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	tickWorld(wa)
	runActionTicks(wa, 8) // 让 Cooldown 计数器有非零值

	bt := ecs.Get[components.BehaviorTree](wa.sim, wolf)
	bt.SetRunningChildOf(behavior.NodeID(3), 2)
	bt.SetIntOf(behavior.NodeID(5), 7)

	snapshot := FullSnapshot(wa.sim)
	var encoded []byte
	for _, entity := range snapshot.Entities {
		if entity.EntityId != uint64(wolf) {
			continue
		}
		for _, component := range entity.Components {
			if component.Component == "BehaviorTree" {
				encoded = component.Data
			}
		}
	}
	if len(encoded) == 0 {
		t.Fatal("快照缺少 BehaviorTree 组件")
	}
	var got game.BehaviorTree
	if err := pb.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != components.TreeKindPredator {
		t.Fatalf("树种类应编码: got=%v", got.Kind)
	}
	foundCursor, foundCounter := false, false
	for _, c := range got.RunningChild {
		if c.NodeId == 3 && c.Child == 2 {
			foundCursor = true
		}
	}
	for _, c := range got.Counters {
		if c.NodeId == 5 && c.Value == 7 {
			foundCounter = true
		}
	}
	if !foundCursor || !foundCounter {
		t.Fatalf("运行态应完整编码: cursor=%v counter=%v", got.RunningChild, got.Counters)
	}
}

// codec 往返：编码再解码应得到等价的运行态。
//
// 走完整快照路径（而不是直接调未导出的 codec）：这样同时验证了
// 组件注册 + 序列化 + 反序列化三件事是自洽的。
func TestBehaviorTreeCodecRoundTrip(t *testing.T) {
	w := ecs.NewWorld()
	components.RegisterBehaviorTree(w)
	e := w.CreateEntity()
	original := components.BehaviorTree{
		Kind:         components.TreeKindPrey,
		RunningChild: map[uint32]uint8{1: 0, 4: 3},
		Counters:     map[uint32]int{2: 9},
	}
	ecs.Add(w, e, original)

	snapshot := FullSnapshot(w)
	var encoded []byte
	for _, entity := range snapshot.Entities {
		if entity.EntityId != uint64(e) {
			continue
		}
		for _, component := range entity.Components {
			if component.Component == "BehaviorTree" {
				encoded = component.Data
			}
		}
	}
	if len(encoded) == 0 {
		t.Fatal("快照缺少 BehaviorTree 组件")
	}
	var got game.BehaviorTree
	if err := pb.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != original.Kind {
		t.Fatalf("Kind 不一致: %v vs %v", got.Kind, original.Kind)
	}
	// 校验运行态逐条还原（解码回组件走的是同一份 proto 定义）。
	var decoded components.BehaviorTree
	decoded.Kind = got.Kind
	decoded.RunningChild = map[uint32]uint8{}
	decoded.Counters = map[uint32]int{}
	for _, c := range got.RunningChild {
		if c != nil {
			decoded.RunningChild[c.NodeId] = uint8(c.Child)
		}
	}
	for _, c := range got.Counters {
		if c != nil {
			decoded.Counters[c.NodeId] = int(c.Value)
		}
	}
	if decoded.Kind != original.Kind {
		t.Fatalf("解码 Kind 不一致: %v", decoded.Kind)
	}
	if len(decoded.RunningChild) != len(original.RunningChild) || decoded.RunningChild[4] != 3 {
		t.Fatalf("RunningChild 不一致: %+v", decoded.RunningChild)
	}
	if decoded.Counters[2] != 9 {
		t.Fatalf("Counters 不一致: %+v", decoded.Counters)
	}
}

// 确定性：同样的初始状态跑两次，行为树驱动的结果必须逐 tick 一致。
func TestBehaviorTreeDeterministic(t *testing.T) {
	run := func() []int {
		wa := NewWorldActor(WorldConfig{})
		for i := 0; i < 4; i++ {
			addTreeCreature(t, wa, i*3, i*3, true, true)
		}
		p := wa.createPlayer("u1")
		ecs.Set(wa.sim, p, components.Position{X: 1, Y: 1})
		var targets []int
		for i := 0; i < 30; i++ {
			tickWorld(wa)
			// 记录每只生物的目标 + 状态（顺序稳定：按实体 id 升序查询）
			var crea []ecs.Entity
			ecs.Query[components.Creature](wa.sim, func(e ecs.Entity, _ *components.Creature) {
				crea = append(crea, e)
			})
			acc := 0
			for _, e := range crea {
				ai := ecs.Get[components.AI](wa.sim, e)
				acc = acc*31 + int(ai.Target) + int(ai.State)
			}
			targets = append(targets, acc)
		}
		return targets
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatalf("两次运行长度不同: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("第 %d tick 行为树结果不一致: %d vs %d", i, a[i], b[i])
		}
	}
}
