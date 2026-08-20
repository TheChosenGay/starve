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

func addTestAttacker(wa *WorldActor, damage int) ecs.Entity {
	attacker := wa.sim.CreateEntity()
	ecs.Add(wa.sim, attacker, components.Position{X: 0, Y: 0})
	ecs.Add(wa.sim, attacker, interactive.Attacker{AttackDamage: damage, AttackRange: 1})
	return attacker
}

func TestAttackBehaviorAndAttackableDoNotInterruptInIsolation(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	attacker := addTestAttacker(wa, 10)
	target := addActionTarget(wa, 0, 1, 100)
	ecs.Add(wa.sim, target, components.ActionState{ActionID: 1, Kind: components.ActionAttack})

	result := interactive.Execute(wa.sim, attacker, target, interactive.IntentAttack)
	if !result.Success || result.Damage == nil || result.Damage.Applied != 10 {
		t.Fatalf("结构化攻击结果=%+v", result)
	}
	if !ecs.Has[components.ActionState](wa.sim, target) {
		t.Fatal("AttackBehavior 不应移除 ActionState")
	}

	damage := ecs.Get[components.Attackable](wa.sim, target).
		ApplyDamage(wa.sim, target, attacker, 1)
	if damage.Applied != 1 || !ecs.Has[components.ActionState](wa.sim, target) {
		t.Fatalf("Attackable 独立调用结果=%+v，且不应移除 ActionState", damage)
	}
}

func TestAttackExecutorInterruptsOnlyAppliedHit(t *testing.T) {
	t.Run("hit", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		attacker := addTestAttacker(wa, 10)
		target := addActionTarget(wa, 0, 1, 100)
		ecs.Add(wa.sim, target, components.ActionState{
			ActionID: 7, Kind: components.ActionAttack, RequestID: 8,
		})
		executor, _ := ecs.Resource[systems.ActionExecutorRegistry](wa.sim).
			Resolve(components.ActionAttack)
		result := executor.Commit(wa.sim, attacker, components.ActionState{
			ActionID: 9, Kind: components.ActionAttack, TargetEntity: target,
		})
		if !result.Committed || ecs.Has[components.ActionState](wa.sim, target) {
			t.Fatalf("命中结果=%+v，应移除目标 ActionState", result)
		}
		events := components.DrainTickEvents(wa.sim)
		if len(events) != 3 ||
			events[0].GetOutcome() == nil ||
			events[1].GetImpact().Result != game.CombatImpactResult_COMBAT_IMPACT_RESULT_HIT ||
			events[2].GetHealthChanged().Delta != -10 {
			t.Fatalf("命中事件顺序应为 outcome/impact/health: %+v", events)
		}
		outcome := events[0].GetOutcome()
		if outcome.Result != game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED ||
			outcome.Reason != game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DAMAGED {
			t.Fatalf("取消 outcome=%+v", outcome)
		}
		if outcome.ActionId != 7 {
			t.Fatalf("取消 action_id=%d, want 7", outcome.ActionId)
		}
	})

	t.Run("blocked", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		attacker := addTestAttacker(wa, 10)
		target := addActionTarget(wa, 0, 1, 100)
		equipDefense(wa, target, 100)
		ecs.Add(wa.sim, target, components.ActionState{ActionID: 7, Kind: components.ActionAttack})
		executor, _ := ecs.Resource[systems.ActionExecutorRegistry](wa.sim).
			Resolve(components.ActionAttack)
		result := executor.Commit(wa.sim, attacker, components.ActionState{
			ActionID: 9, Kind: components.ActionAttack, TargetEntity: target,
		})
		if !result.Committed || !ecs.Has[components.ActionState](wa.sim, target) {
			t.Fatalf("格挡结果=%+v，不应移除目标 ActionState", result)
		}
		events := components.DrainTickEvents(wa.sim)
		if len(events) != 1 ||
			events[0].GetImpact().Result != game.CombatImpactResult_COMBAT_IMPACT_RESULT_BLOCKED {
			t.Fatalf("格挡 events=%+v", events)
		}
	})
}

func TestAttackCommitMissesWhenTargetInvalidAfterWindup(t *testing.T) {
	tests := []struct {
		name       string
		invalidate func(*WorldActor, ecs.Entity)
	}{
		{
			name: "moved_out_of_range",
			invalidate: func(wa *WorldActor, target ecs.Entity) {
				ecs.Set(wa.sim, target, components.Position{X: 20, Y: 20})
			},
		},
		{
			name: "already_dead",
			invalidate: func(wa *WorldActor, target ecs.Entity) {
				ecs.Get[components.Health](wa.sim, target).Cur = 0
				ecs.Add(wa.sim, target, components.Dead{Reason: "test"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa := NewWorldActor(WorldConfig{})
			attacker := addTestAttacker(wa, 10)
			target := addActionTarget(wa, 0, 1, 100)
			ecs.Add(wa.sim, target, components.ActionState{
				ActionID: 100, Kind: components.ActionAttack,
				Phase: components.ActionWindup, CommitTick: 1000, EndTick: 1008,
			})
			ecs.Add(wa.sim, attacker, components.ActionState{
				ActionID: 101, Kind: components.ActionAttack, TargetEntity: target,
				Phase: components.ActionWindup, CommitTick: 4, EndTick: 8,
			})
			tt.invalidate(wa, target)
			before := ecs.Get[components.Health](wa.sim, target).Cur
			ecs.Resource[components.DayCycle](wa.sim).Phase = 4
			wa.sim.DrainDirtySorted()
			components.DrainTickEvents(wa.sim)

			(&systems.ActionSystem{}).Update(wa.sim, 0)

			if got := ecs.Get[components.Health](wa.sim, target).Cur; got != before {
				t.Fatalf("MISS 后 hp=%d, want %d", got, before)
			}
			if !ecs.Has[components.ActionState](wa.sim, target) {
				t.Fatal("MISS 不应由 AttackExecutor 打断目标 ActionState")
			}
			events := components.DrainTickEvents(wa.sim)
			if len(events) != 1 ||
				events[0].GetImpact().Result != game.CombatImpactResult_COMBAT_IMPACT_RESULT_MISS {
				t.Fatalf("MISS events=%+v", events)
			}
			if outcomes := drainActionOutcomes(wa); len(outcomes) != 0 {
				t.Fatalf("MISS 不应产生目标取消 outcome: %+v", outcomes)
			}
		})
	}
}

func TestDeltaContainsAtomicAttackChangesAndEvents(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	attacker := addTestAttacker(wa, 10)
	target := addActionTarget(wa, 0, 1, 100)
	ecs.Add(wa.sim, target, components.ActionState{ActionID: 1, Kind: components.ActionAttack})
	wa.sim.DrainDirtySorted()

	executor, _ := ecs.Resource[systems.ActionExecutorRegistry](wa.sim).
		Resolve(components.ActionAttack)
	executor.Commit(wa.sim, attacker, components.ActionState{
		ActionID: 2, Kind: components.ActionAttack, TargetEntity: target,
	})
	events := components.DrainTickEvents(wa.sim)
	delta := DeltaSnapshot(wa.sim, wa.sim.DrainDirtySorted(), nil)
	delta.Events = events

	if len(delta.Events) != 3 {
		t.Fatalf("delta events=%d, want 3", len(delta.Events))
	}
	if delta.Events[0].GetOutcome() == nil ||
		delta.Events[1].GetImpact() == nil ||
		delta.Events[2].GetHealthChanged() == nil {
		t.Fatalf("delta 事件顺序应为 outcome/impact/health: %+v", delta.Events)
	}
	if !deltaHasComponent(delta, uint64(target), "Health") {
		t.Fatal("delta 缺少同批 Health 变化")
	}
	if !deltaRemovedComponent(delta, uint64(target), "ActionState") {
		t.Fatal("delta 缺少同批 ActionState removal")
	}
	if again := components.DrainTickEvents(wa.sim); len(again) != 0 {
		t.Fatalf("事件重复 drain: %+v", again)
	}
	components.EmitHealthChanged(
		wa.sim, target, attacker, 1,
		game.HealthChangeCause_HEALTH_CHANGE_CAUSE_HEALING, 0,
	)
	next := components.DrainTickEvents(wa.sim)
	if len(next) != 1 || next[0].EventId <= events[len(events)-1].EventId {
		t.Fatalf("event_id 未跨 drain 单调递增: old=%d next=%+v", events[len(events)-1].EventId, next)
	}

	wire, err := pb.Marshal(delta)
	if err != nil {
		t.Fatal(err)
	}
	var decoded game.SnapshotDelta
	if err := pb.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Events) != 3 || decoded.Events[0].EventId == 0 {
		t.Fatalf("protobuf events=%+v", decoded.Events)
	}
}

func TestStarvationEmitsCauseWithoutInterrupt(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	target := wa.createPlayer("u1")
	ecs.Set(wa.sim, target, components.Hunger{Level: 0})
	ecs.Set(wa.sim, target, components.Health{Cur: 10, Max: 10})
	ecs.Add(wa.sim, target, components.ActionState{ActionID: 1, Kind: components.ActionAttack})

	(&systems.StarvationSystem{HealthDrain: 2}).Update(wa.sim, 0)
	if !ecs.Has[components.ActionState](wa.sim, target) {
		t.Fatal("饥饿伤害默认不应打断动作")
	}
	events := components.DrainTickEvents(wa.sim)
	if len(events) != 1 ||
		events[0].GetHealthChanged().Cause != game.HealthChangeCause_HEALTH_CHANGE_CAUSE_STARVATION ||
		events[0].GetHealthChanged().Delta != -2 {
		t.Fatalf("starvation events=%+v", events)
	}
}

func TestHealingEmitsActualClampedDelta(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	target := wa.createPlayer("u1")
	ecs.Set(wa.sim, target, components.Health{Cur: 95, Max: 100})

	applied := components.ApplyHealthDelta(
		wa.sim, target, target, 20,
		game.HealthChangeCause_HEALTH_CHANGE_CAUSE_HEALING, 0,
	)
	events := components.DrainTickEvents(wa.sim)
	if applied != 5 || len(events) != 1 ||
		events[0].GetHealthChanged().Delta != 5 ||
		events[0].GetHealthChanged().Cause != game.HealthChangeCause_HEALTH_CHANGE_CAUSE_HEALING {
		t.Fatalf("healing applied/events=%d/%+v", applied, events)
	}
}

func deltaHasComponent(delta *game.SnapshotDelta, entity uint64, component string) bool {
	for _, state := range delta.Entities {
		if state.EntityId != entity {
			continue
		}
		for _, entry := range state.Components {
			if entry.Component == component {
				return true
			}
		}
	}
	return false
}

func deltaRemovedComponent(delta *game.SnapshotDelta, entity uint64, component string) bool {
	for _, removed := range delta.RemovedComponents {
		if removed.EntityId != entity {
			continue
		}
		for _, name := range removed.Components {
			if name == component {
				return true
			}
		}
	}
	return false
}
