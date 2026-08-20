package world

import (
	"testing"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/systems"
	game "starve/pkg/proto/game"
)

func addActionTarget(wa *WorldActor, x, y, hp int) ecs.Entity {
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: hp, Max: hp})
	ecs.Add(wa.sim, e, components.Attackable{})
	return e
}

func runActionTicks(wa *WorldActor, ticks int) {
	for i := 0; i < ticks; i++ {
		tickWorld(wa)
	}
}

func TestAttackCommitsOnlyAtCommitTick(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AttackDamage: 10})
	player := wa.createPlayer("u1")
	target := addActionTarget(wa, 0, 1, 100)

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: target}})
	tickWorld(wa)
	state := ecs.Get[components.ActionState](wa.sim, player)
	if state.CommitTick-state.PhaseStartTick != 8 ||
		state.EndTick-state.CommitTick != 8 {
		t.Fatalf(
			"attack timing windup/recovery=(%d,%d), want (8,8)",
			state.CommitTick-state.PhaseStartTick,
			state.EndTick-state.CommitTick,
		)
	}
	runActionTicks(wa, 7)
	if hp := ecs.Get[components.Health](wa.sim, target).Cur; hp != 100 {
		t.Fatalf("commit tick 前 hp=%d, want 100", hp)
	}
	tickWorld(wa)
	if hp := ecs.Get[components.Health](wa.sim, target).Cur; hp != 90 {
		t.Fatalf("commit tick hp=%d, want 90", hp)
	}
	if state := ecs.Get[components.ActionState](wa.sim, player); state.Phase != components.ActionRecovery {
		t.Fatalf("commit 后 phase=%v, want recovery", state.Phase)
	}
}

func TestMoveCancelsAttackBeforeCommit(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AttackDamage: 10, MoveSpeed: 20})
	player := wa.createPlayer("u1")
	target := addActionTarget(wa, 0, 1, 100)

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: target}})
	tickWorld(wa)
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1}})
	tickWorld(wa)

	if ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("移动应取消动作")
	}
	if hp := ecs.Get[components.Health](wa.sim, target).Cur; hp != 100 {
		t.Fatalf("取消后 hp=%d, want 100", hp)
	}
	if p := ecs.Get[components.Position](wa.sim, player); p.X != 1 {
		t.Fatalf("取消动作同 tick 应移动: x=%d, want 1", p.X)
	}
	runActionTicks(wa, 9)
	if hp := ecs.Get[components.Health](wa.sim, target).Cur; hp != 100 {
		t.Fatalf("原 commit tick 不应伤害: hp=%d", hp)
	}
}

func TestControlArrivalOrderIsPredictable(t *testing.T) {
	t.Run("attack_then_move", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		target := addActionTarget(wa, 0, 1, 100)
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: target}})
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1}})
		tickWorld(wa)
		if ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("后到的 Move 应取消先到的 Attack")
		}
	})

	t.Run("move_then_attack", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		target := addActionTarget(wa, 0, 1, 100)
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1}})
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: target}})
		tickWorld(wa)
		if !ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("后到的 Attack 应被接纳")
		}
		mv := ecs.Get[components.Moveable](wa.sim, player)
		if mv.DirX != 0 || mv.DirY != 0 {
			t.Fatalf("动作开始应停止移动: dir=(%d,%d)", mv.DirX, mv.DirY)
		}
	})
}

func TestPlayerAndNPCShareAttackAction(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AttackDamage: 10})
	player := wa.createPlayer("u1")
	npc := addActionTarget(wa, 0, 1, 30)
	ecs.Add(wa.sim, npc, components.Moveable{Speed: 10})
	ecs.Add(wa.sim, npc, components.Creature{Threats: map[ecs.Entity]int32{player: 10}})
	ecs.Add(wa.sim, npc, components.AI{Target: player})
	ecs.Add(wa.sim, npc, interactive.Attacker{AttackDamage: 7, AttackRange: 2, AttackCooldown: 6})

	systems.EnqueueControl(wa.sim, systems.StartActionIntent(player, components.ActionAttack, npc, 11))
	systems.EnqueueControl(wa.sim, systems.StartActionIntent(npc, components.ActionAttack, player, 0))
	tickWorld(wa)

	for _, e := range []ecs.Entity{player, npc} {
		if !ecs.Has[components.ActionState](wa.sim, e) {
			t.Fatalf("实体 %d 未进入共享 ActionState", e)
		}
	}
	if got := ecs.Get[components.AI](wa.sim, npc).Cooldown; got != 6 {
		t.Fatalf("NPC 成功接纳后 cooldown=%d, want 6", got)
	}
}

func TestSimultaneousAttacksBothCommit(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	a := addActionTarget(wa, 0, 0, 10)
	b := addActionTarget(wa, 0, 1, 10)
	for _, e := range []ecs.Entity{a, b} {
		ecs.Add(wa.sim, e, components.Moveable{Speed: 10})
		ecs.Add(wa.sim, e, interactive.Attacker{AttackDamage: 10, AttackRange: 1})
	}
	systems.EnqueueControl(wa.sim, systems.StartActionIntent(a, components.ActionAttack, b, 1))
	systems.EnqueueControl(wa.sim, systems.StartActionIntent(b, components.ActionAttack, a, 2))
	tickWorld(wa)
	runActionTicks(wa, 8)

	if ahp, bhp := ecs.Get[components.Health](wa.sim, a).Cur, ecs.Get[components.Health](wa.sim, b).Cur; ahp != 0 || bhp != 0 {
		t.Fatalf("同时攻击应双方命中: hp=(%d,%d)", ahp, bhp)
	}
}

func TestActionStateSnapshotCodecAndRemoval(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	target := addActionTarget(wa, 0, 1, 100)
	systems.EnqueueControl(wa.sim, systems.StartActionIntent(player, components.ActionAttack, target, 42))
	tickWorld(wa)

	snapshot := FullSnapshot(wa.sim)
	var encoded []byte
	for _, entity := range snapshot.Entities {
		if entity.EntityId != uint64(player) {
			continue
		}
		for _, component := range entity.Components {
			if component.Component == "ActionState" {
				encoded = component.Data
			}
		}
	}
	if len(encoded) == 0 {
		t.Fatal("快照缺少 ActionState")
	}
	var got game.ActionState
	if err := pb.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Kind != game.ActionKind_ACTION_KIND_ATTACK || got.TargetEntity != uint64(target) ||
		got.RequestId != 42 || got.CommitTick == 0 || got.EndTick <= got.CommitTick {
		t.Fatalf("ActionState codec 内容错误: kind=%v target=%d request=%d commit=%d end=%d",
			got.Kind, got.TargetEntity, got.RequestId, got.CommitTick, got.EndTick)
	}

	wa.sim.DrainEvents()
	systems.EnqueueControl(wa.sim, systems.MoveIntent(player, 1, 0))
	tickWorld(wa)
	removed := false
	for _, event := range wa.sim.DrainEvents() {
		if event.Kind == ecs.ComponentRemoved && event.Entity == player &&
			event.Component == ecs.ComponentIDOf[components.ActionState](wa.sim) {
			removed = true
		}
	}
	if !removed || ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("取消动作必须产生组件移除事件")
	}
}
