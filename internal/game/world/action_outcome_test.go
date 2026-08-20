package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

func drainActionOutcomes(wa *WorldActor) []*game.ActionOutcome {
	var outcomes []*game.ActionOutcome
	for _, event := range components.DrainTickEvents(wa.sim) {
		if outcome := event.GetOutcome(); outcome != nil {
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes
}

func requireSingleOutcome(
	t *testing.T,
	wa *WorldActor,
	result game.ActionOutcomeResult,
	reason game.ActionOutcomeReason,
) *game.ActionOutcome {
	t.Helper()
	outcomes := drainActionOutcomes(wa)
	if len(outcomes) != 1 {
		t.Fatalf("outcome 数=%d, want 1: %+v", len(outcomes), outcomes)
	}
	outcome := outcomes[0]
	if outcome.Result != result || outcome.Reason != reason {
		t.Fatalf("result/reason=(%v,%v), want (%v,%v)", outcome.Result, outcome.Reason, result, reason)
	}
	if outcome.ActionId == 0 || outcome.Kind == game.ActionKind_ACTION_KIND_UNSPECIFIED {
		t.Fatalf("outcome 丢失 action_id/kind: %+v", outcome)
	}
	return outcome
}

func TestActionOutcomeCompletedExactlyOnce(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	target := addActionTarget(wa, 0, 1, 100)
	wa.cmds.Handle(Command{
		UID: "u1", Seq: 77, Kind: CommandAttack,
		Data: AttackData{Attacker: player, Target: target},
	})
	components.BeginTickEvents(wa.sim, 42)
	runActionTicks(wa, 17)

	outcome := requireSingleOutcome(
		t, wa,
		game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_COMPLETED,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSPECIFIED,
	)
	if outcome.EntityId != uint64(player) || outcome.RequestId != 77 || outcome.Tick != 42 {
		t.Fatalf("completed outcome 字段错误: %+v", outcome)
	}
	runActionTicks(wa, 2)
	if outcomes := drainActionOutcomes(wa); len(outcomes) != 0 {
		t.Fatalf("完成后重复 outcome: %+v", outcomes)
	}
}

func TestActionOutcomeCancellationReasonsExactlyOnce(t *testing.T) {
	tests := []struct {
		name   string
		reason game.ActionOutcomeReason
		cancel func(*WorldActor, ecs.Entity)
	}{
		{
			name: "moved", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_MOVED,
			cancel: func(wa *WorldActor, actor ecs.Entity) {
				systems.EnqueueControl(wa.sim, systems.MoveIntent(actor, 1, 0))
				tickWorld(wa)
			},
		},
		{
			name: "damaged", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DAMAGED,
			cancel: func(wa *WorldActor, actor ecs.Entity) {
				attacker := wa.createPlayer("u2")
				executor, _ := ecs.Resource[systems.ActionExecutorRegistry](wa.sim).
					Resolve(components.ActionAttack)
				executor.Commit(wa.sim, attacker, components.ActionState{
					ActionID: 999, Kind: components.ActionAttack, TargetEntity: actor,
				})
			},
		},
		{
			name: "dead", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DEAD,
			cancel: func(wa *WorldActor, actor ecs.Entity) {
				ecs.Get[components.Health](wa.sim, actor).Cur = 0
				(&systems.DeathSystem{}).Update(wa.sim, 0)
			},
		},
		{
			name: "explicit", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT,
			cancel: func(wa *WorldActor, actor ecs.Entity) {
				systems.EnqueueControl(wa.sim, systems.ControlIntent{
					Kind: systems.ControlCancelAction, Actor: actor,
				})
				tickWorld(wa)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa := NewWorldActor(WorldConfig{})
			player := wa.createPlayer("u1")
			target := addActionTarget(wa, 0, 1, 100)
			systems.EnqueueControl(wa.sim, systems.StartActionIntent(
				player, components.ActionAttack, target, 9,
			))
			tickWorld(wa)
			drainActionOutcomes(wa)

			tt.cancel(wa, player)
			outcome := requireSingleOutcome(
				t, wa,
				game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED,
				tt.reason,
			)
			if outcome.RequestId != 9 {
				t.Fatalf("request_id=%d, want 9", outcome.RequestId)
			}
			runActionTicks(wa, 10)
			if outcomes := drainActionOutcomes(wa); len(outcomes) != 0 {
				t.Fatalf("取消后重复 outcome: %+v", outcomes)
			}
		})
	}
}

func TestActionOutcomeRejectionsExactlyOnce(t *testing.T) {
	tests := []struct {
		name   string
		reason game.ActionOutcomeReason
		setup  func(*WorldActor) systems.ControlIntent
	}{
		{
			name: "invalid_actor", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_ACTOR,
			setup: func(wa *WorldActor) systems.ControlIntent {
				return systems.StartActionIntent(999, components.ActionAttack, 1, 5)
			},
		},
		{
			name: "invalid_target", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET,
			setup: func(wa *WorldActor) systems.ControlIntent {
				player := wa.createPlayer("u1")
				return systems.StartActionIntent(player, components.ActionAttack, 999, 5)
			},
		},
		{
			name: "unsupported", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSUPPORTED,
			setup: func(wa *WorldActor) systems.ControlIntent {
				player := wa.createPlayer("u1")
				return systems.StartActionIntent(player, components.ActionKind(99), 0, 5)
			},
		},
		{
			name: "busy", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_BUSY,
			setup: func(wa *WorldActor) systems.ControlIntent {
				player := wa.createPlayer("u1")
				target := addActionTarget(wa, 0, 1, 100)
				systems.EnqueueControl(wa.sim, systems.StartActionIntent(
					player, components.ActionAttack, target, 1,
				))
				tickWorld(wa)
				drainActionOutcomes(wa)
				return systems.StartActionIntent(player, components.ActionAttack, target, 5)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa := NewWorldActor(WorldConfig{})
			intent := tt.setup(wa)
			systems.EnqueueControl(wa.sim, intent)
			tickWorld(wa)
			outcome := requireSingleOutcome(
				t, wa,
				game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_REJECTED,
				tt.reason,
			)
			if outcome.RequestId != 5 {
				t.Fatalf("request_id=%d, want 5", outcome.RequestId)
			}
			tickWorld(wa)
			if outcomes := drainActionOutcomes(wa); len(outcomes) != 0 {
				t.Fatalf("拒绝后重复 outcome: %+v", outcomes)
			}
		})
	}
}

func TestDrainEffectsDoesNotRouteDeprecatedActionOutcome(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wa.sim.Emit(&game.ActionOutcome{
		EntityId: 1,
		ActionId: 2,
		Kind:     game.ActionKind_ACTION_KIND_PICK,
		Result:   game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_COMPLETED,
	})
	wa.sim.Emit(&proto.CraftDone{Uid: "u1", Success: true})
	wa.drainEffects()
	if len(wa.outbox) != 1 {
		t.Fatalf("outbox 数=%d, want 1", len(wa.outbox))
	}
	push, ok := wa.outbox[0].(PushEffect)
	if !ok || push.Route != proto.RouteCraftDone {
		t.Fatalf("独立 Outcome 不应 push，CraftDone route=%v", wa.outbox[0])
	}
}
