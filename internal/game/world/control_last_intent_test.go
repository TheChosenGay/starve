package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	game "starve/pkg/proto/game"
)

func TestControlLastIntentWinsAcrossMoveAndAction(t *testing.T) {
	t.Run("attack then move", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		target := addActionTarget(wa, 0, 1, 100)
		components.BeginTickEvents(wa.sim, 1)

		wa.cmds.Handle(Command{
			UID: "u1", Seq: 1, RequestID: 101, Kind: CommandAttack,
			Data: AttackData{Attacker: player, Target: target},
		})
		wa.cmds.Handle(Command{
			UID: "u1", Seq: 2, Kind: CommandMove,
			Data: MoveData{Entity: player, DX: 1},
		})
		tickWorld(wa)

		results := ecs.Resource[systems.ControlQueue](wa.sim).Results
		if len(results) != 2 || !results[0].Superseded || !results[1].Accepted {
			t.Fatalf("results=%+v", results)
		}
		if ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("被覆盖的攻击不应创建 ActionState")
		}
		if events := components.DrainTickEvents(wa.sim); len(events) != 0 {
			t.Fatalf("被覆盖攻击不应产生 outcome: %+v", events)
		}
	})

	t.Run("move then attack", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		target := addActionTarget(wa, 0, 1, 100)
		components.BeginTickEvents(wa.sim, 1)

		wa.cmds.Handle(Command{
			UID: "u1", Seq: 1, Kind: CommandMove,
			Data: MoveData{Entity: player, DX: 1},
		})
		wa.cmds.Handle(Command{
			UID: "u1", Seq: 2, RequestID: 202, Kind: CommandAttack,
			Data: AttackData{Attacker: player, Target: target},
		})
		tickWorld(wa)

		results := ecs.Resource[systems.ControlQueue](wa.sim).Results
		if len(results) != 2 || !results[0].Superseded || !results[1].Accepted {
			t.Fatalf("results=%+v", results)
		}
		state := ecs.Get[components.ActionState](wa.sim, player)
		if state.TargetEntity != target || state.RequestID != 202 {
			t.Fatalf("action=%+v", state)
		}
		if events := components.DrainTickEvents(wa.sim); len(events) != 0 {
			t.Fatalf("被覆盖移动不应产生中间 outcome: %+v", events)
		}
	})
}

func TestControlLastActionWinsAndInvalidDoesNotFallback(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	first := addActionTarget(wa, 0, 1, 100)
	last := addActionTarget(wa, 1, 0, 100)

	systems.EnqueueControl(wa.sim, systems.StartActionIntent(
		player, components.ActionAttack, first, 1, 101,
	))
	systems.EnqueueControl(wa.sim, systems.StartActionIntent(
		player, components.ActionAttack, last, 2, 202,
	))
	tickWorld(wa)
	results := ecs.Resource[systems.ControlQueue](wa.sim).Results
	if len(results) != 2 || !results[0].Superseded || !results[1].Accepted {
		t.Fatalf("action→action results=%+v", results)
	}
	if got := ecs.Get[components.ActionState](wa.sim, player).TargetEntity; got != last {
		t.Fatalf("赢家 target=%d, want %d", got, last)
	}

	components.TryInterrupt(
		wa.sim, player,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT,
	)
	components.DrainTickEvents(wa.sim)
	systems.EnqueueControl(wa.sim, systems.StartActionIntent(
		player, components.ActionAttack, first, 3, 303,
	))
	systems.EnqueueControl(wa.sim, systems.StartActionIntent(
		player, components.ActionAttack, ecs.Entity(999999), 4, 404,
	))
	tickWorld(wa)
	results = ecs.Resource[systems.ControlQueue](wa.sim).Results
	if !results[0].Superseded || results[1].Accepted ||
		results[1].Reason != systems.ControlRejectedInvalidTarget {
		t.Fatalf("invalid last results=%+v", results)
	}
	if ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("最后赢家无效时不得回退到更早动作")
	}
	events := components.DrainTickEvents(wa.sim)
	if len(events) != 1 || events[0].GetOutcome() == nil ||
		events[0].GetOutcome().RequestId != 404 {
		t.Fatalf("只应有最后赢家的 rejected outcome: %+v", events)
	}
}

func TestControlCancelParticipatesInLastIntentWins(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	target := addActionTarget(wa, 0, 1, 100)
	systems.EnqueueControl(wa.sim, systems.StartActionIntent(
		player, components.ActionAttack, target, 1, 11,
	))
	tickWorld(wa)
	components.DrainTickEvents(wa.sim)

	wa.cmds.Handle(Command{
		UID: "u1", Seq: 2, Kind: CommandMove,
		Data: MoveData{Entity: player, DX: 1},
	})
	wa.cmds.Handle(Command{
		UID: "u1", Seq: 3, Kind: CommandCancelCraft,
		Data: CancelCraftData{Player: player},
	})
	tickWorld(wa)

	results := ecs.Resource[systems.ControlQueue](wa.sim).Results
	if len(results) != 2 || !results[0].Superseded || !results[1].Accepted {
		t.Fatalf("cancel results=%+v", results)
	}
	events := components.DrainTickEvents(wa.sim)
	if len(events) != 1 || events[0].GetOutcome() == nil ||
		events[0].GetOutcome().Reason != game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT {
		t.Fatalf("cancel outcome=%+v", events)
	}
}
