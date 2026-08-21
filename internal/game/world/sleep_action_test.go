package world

import (
	"encoding/json"
	"testing"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	game "starve/pkg/proto/game"
)

func addSleepCampfire(wa *WorldActor, x, y int, placed bool) ecs.Entity {
	entity := wa.sim.CreateEntity()
	ecs.Add(wa.sim, entity, components.Building{
		Kind: components.BuildingCampfire, Width: 1, Height: 1, Placed: placed,
	})
	if placed {
		ecs.Add(wa.sim, entity, components.Position{X: x, Y: y})
	}
	return entity
}

func startSleep(wa *WorldActor, player ecs.Entity, requestID uint64) {
	wa.cmds.Handle(Command{
		UID: "u1", RequestID: requestID, Kind: CommandSleep,
		Data: SleepData{Player: player},
	})
	tickWorld(wa)
}

func setSleepVitals(wa *WorldActor, player ecs.Entity, health, maxHealth, hunger, rate int) {
	ecs.Set(wa.sim, player, components.Health{Cur: health, Max: maxHealth})
	ecs.Set(wa.sim, player, components.Hunger{Level: hunger, Rate: rate})
}

func TestSleepSelectsNearestPlacedCampfire(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	far := addSleepCampfire(wa, 0, 3, true)
	near := addSleepCampfire(wa, 0, 1, true)
	addSleepCampfire(wa, 0, 0, false)
	workstation := wa.sim.CreateEntity()
	ecs.Add(wa.sim, workstation, components.Workstation{Type: components.StationWorkbench})
	ecs.Add(wa.sim, workstation, components.Position{X: 0, Y: 0})

	startSleep(wa, player, 41)

	state := ecs.Get[components.ActionState](wa.sim, player)
	if state.Kind != components.ActionSleep || state.TargetEntity != near {
		t.Fatalf("sleep state kind/target=(%v,%d), want (%v,%d); far=%d",
			state.Kind, state.TargetEntity, components.ActionSleep, near, far)
	}
	if state.CommitTick-state.PhaseStartTick != systems.SleepPulseTicks ||
		state.EndTick != state.CommitTick {
		t.Fatalf("sleep timing start=%d commit=%d end=%d",
			state.PhaseStartTick, state.CommitTick, state.EndTick)
	}
}

func TestSleepUsesCampfireFootprintAndAcceptsMapCampfire(t *testing.T) {
	t.Run("2x2 building edge", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})
		campfire := wa.sim.CreateEntity()
		ecs.Add(wa.sim, campfire, components.Building{
			Kind: components.BuildingCampfire, Width: 2, Height: 2, Placed: true,
		})
		ecs.Add(wa.sim, campfire, components.Position{X: 0, Y: 0})

		startSleep(wa, player, 42)

		state := ecs.Get[components.ActionState](wa.sim, player)
		if state.TargetEntity != campfire {
			t.Fatalf("sleep target=%d, want 2x2 campfire %d", state.TargetEntity, campfire)
		}
	})

	t.Run("map workstation campfire", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		campfire := wa.sim.CreateEntity()
		ecs.Add(wa.sim, campfire, components.Workstation{Type: components.StationCampfire})
		ecs.Add(wa.sim, campfire, components.Position{X: 0, Y: 1})
		ecs.Add(wa.sim, campfire, components.Block{Width: 1, Height: 1})

		startSleep(wa, player, 43)

		state := ecs.Get[components.ActionState](wa.sim, player)
		if state.TargetEntity != campfire {
			t.Fatalf("sleep target=%d, want map campfire %d", state.TargetEntity, campfire)
		}
	})
}

func TestSleepRejectsWithoutEligibleCampfire(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*WorldActor)
	}{
		{name: "none"},
		{name: "unplaced", setup: func(wa *WorldActor) {
			addSleepCampfire(wa, 0, 0, false)
		}},
		{name: "out_of_range", setup: func(wa *WorldActor) {
			addSleepCampfire(wa, systems.SleepRange+1, 0, true)
		}},
		{name: "non_campfire_workstation", setup: func(wa *WorldActor) {
			entity := wa.sim.CreateEntity()
			ecs.Add(wa.sim, entity, components.Workstation{Type: components.StationWorkbench})
			ecs.Add(wa.sim, entity, components.Position{X: 0, Y: 0})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wa := NewWorldActor(WorldConfig{})
			player := wa.createPlayer("u1")
			if test.setup != nil {
				test.setup(wa)
			}
			startSleep(wa, player, 52)
			if ecs.Has[components.ActionState](wa.sim, player) {
				t.Fatal("无合格营火时不应进入 ActionState")
			}
			outcome := requireSingleOutcome(
				t, wa,
				game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_REJECTED,
				game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET,
			)
			if outcome.Kind != components.ActionSleep || outcome.RequestId != 52 {
				t.Fatalf("rejected outcome=%+v", outcome)
			}
		})
	}
}

func TestSleepRepeatsPulsesWithNormalTicksSnapshotAndNoCompletedOutcome(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	campfire := addSleepCampfire(wa, 0, 1, true)
	setSleepVitals(wa, player, 50, 60, 100, 0)
	startPhase := ecs.Resource[components.DayCycle](wa.sim).Phase

	startSleep(wa, player, 63)
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
	var state game.ActionState
	if len(encoded) == 0 || pb.Unmarshal(encoded, &state) != nil {
		t.Fatal("sleep ActionState 未进入快照")
	}
	if state.Kind != game.ActionKind_ACTION_KIND_SLEEP ||
		state.TargetEntity != uint64(campfire) || state.RequestId != 63 {
		t.Fatalf("snapshot sleep state=%+v", &state)
	}

	runActionTicks(wa, int(systems.SleepPulseTicks)-1)
	if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 50 {
		t.Fatalf("commit 前 health=%d, want 50", hp)
	}
	tickWorld(wa)

	if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 52 {
		t.Fatalf("第一周期后 health=%d, want 52", hp)
	}
	if hunger := ecs.Get[components.Hunger](wa.sim, player).Level; hunger != 98 {
		t.Fatalf("第一周期后 hunger=%d, want 98", hunger)
	}
	first := *ecs.Get[components.ActionState](wa.sim, player)
	if first.ActionID != state.ActionId ||
		first.CommitTick-first.PhaseStartTick != systems.SleepPulseTicks {
		t.Fatalf("第一周期未重装同一 ActionState: %+v", first)
	}

	runActionTicks(wa, int(systems.SleepPulseTicks))
	if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 54 {
		t.Fatalf("第二周期后 health=%d, want 54", hp)
	}
	if hunger := ecs.Get[components.Hunger](wa.sim, player).Level; hunger != 96 {
		t.Fatalf("第二周期后 hunger=%d, want 96", hunger)
	}
	second := ecs.Get[components.ActionState](wa.sim, player)
	if second.ActionID != first.ActionID ||
		second.PhaseStartTick != first.CommitTick ||
		second.CommitTick-second.PhaseStartTick != systems.SleepPulseTicks {
		t.Fatalf("第二周期未延续同一 ActionState: first=%+v second=%+v", first, *second)
	}
	if phase := ecs.Resource[components.DayCycle](wa.sim).Phase; phase-startPhase != 201 {
		t.Fatalf("day cycle advanced=%d, want 201 normal ticks", phase-startPhase)
	}
	if outcomes := drainActionOutcomes(wa); len(outcomes) != 0 {
		t.Fatalf("持续睡眠不应发 completed outcome: %+v", outcomes)
	}
}

func TestSleepAtFullHealthConsumesHungerAndStarvationDamageInterrupts(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	addSleepCampfire(wa, 0, 1, true)
	setSleepVitals(wa, player, 100, 100, 1, 0)
	startSleep(wa, player, 64)
	drainActionOutcomes(wa)

	runActionTicks(wa, int(systems.SleepPulseTicks))

	if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 100 {
		t.Fatalf("满生命应保持 100，got %d", hp)
	}
	if hunger := ecs.Get[components.Hunger](wa.sim, player).Level; hunger != 0 {
		t.Fatalf("睡眠消耗后 hunger=%d, want 0", hunger)
	}
	if !ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("饥饿实际造成伤害前睡眠应继续")
	}

	runActionTicks(wa, 20)

	if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 99 {
		t.Fatalf("饥饿伤害后 health=%d, want 99", hp)
	}
	if ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("饥饿造成实际伤害后应中断睡眠")
	}
	requireSingleOutcome(
		t, wa,
		game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DAMAGED,
	)
}

func TestSleepInterruptionsGiveNoBenefit(t *testing.T) {
	tests := []struct {
		name   string
		reason game.ActionOutcomeReason
		cancel func(*WorldActor, ecs.Entity)
	}{
		{
			name: "move", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_MOVED,
			cancel: func(wa *WorldActor, player ecs.Entity) {
				systems.EnqueueControl(wa.sim, systems.MoveIntent(player, 1, 0, 0))
				tickWorld(wa)
			},
		},
		{
			name: "actual_damage", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DAMAGED,
			cancel: func(wa *WorldActor, player ecs.Entity) {
				attacker := wa.createPlayer("u2")
				executor, _ := ecs.Resource[systems.ActionExecutorRegistry](wa.sim).
					Resolve(components.ActionAttack)
				executor.Commit(wa.sim, attacker, components.ActionState{
					ActionID: 999, Kind: components.ActionAttack, TargetEntity: player,
				})
			},
		},
		{
			name: "environmental_damage", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DAMAGED,
			cancel: func(wa *WorldActor, player ecs.Entity) {
				components.ApplyHealthDelta(
					wa.sim, player, 0, -1,
					game.HealthChangeCause_HEALTH_CHANGE_CAUSE_WEATHER,
					0,
				)
			},
		},
		{
			name: "death", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DEAD,
			cancel: func(wa *WorldActor, player ecs.Entity) {
				ecs.Get[components.Health](wa.sim, player).Cur = 0
				(&systems.DeathSystem{}).Update(wa.sim, 0)
			},
		},
		{
			name: "explicit", reason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT,
			cancel: func(wa *WorldActor, player ecs.Entity) {
				wa.cmds.Handle(Command{
					UID: "u1", Kind: CommandCancelAction,
					Data: CancelActionData{Player: player},
				})
				tickWorld(wa)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wa := NewWorldActor(WorldConfig{})
			player := wa.createPlayer("u1")
			addSleepCampfire(wa, 0, 1, true)
			setSleepVitals(wa, player, 40, 100, 80, 0)
			startSleep(wa, player, 74)
			drainActionOutcomes(wa)

			test.cancel(wa, player)
			if ecs.Has[components.ActionState](wa.sim, player) {
				t.Fatal("中断后仍有 ActionState")
			}
			if test.name != "actual_damage" && test.name != "environmental_damage" &&
				test.name != "death" {
				if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 40 {
					t.Fatalf("中断不应治疗: health=%d", hp)
				}
			}
			if hunger := ecs.Get[components.Hunger](wa.sim, player).Level; hunger != 80 {
				t.Fatalf("中断不应额外消耗 hunger: %d", hunger)
			}
			requireSingleOutcome(
				t, wa,
				game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED,
				test.reason,
			)
			runActionTicks(wa, int(systems.SleepPulseTicks)+1)
			if hunger := ecs.Get[components.Hunger](wa.sim, player).Level; hunger != 80 {
				t.Fatalf("原 commit tick 不应补扣 hunger: %d", hunger)
			}
		})
	}
}

func TestCancelSleepAfterPulseStopsLaterPulses(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	addSleepCampfire(wa, 0, 1, true)
	setSleepVitals(wa, player, 40, 100, 80, 0)
	startSleep(wa, player, 75)
	drainActionOutcomes(wa)
	runActionTicks(wa, int(systems.SleepPulseTicks))

	wa.cmds.Handle(Command{
		UID: "u1", Kind: CommandCancelAction,
		Data: CancelActionData{Player: player},
	})
	tickWorld(wa)
	requireSingleOutcome(
		t, wa,
		game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT,
	)
	runActionTicks(wa, int(systems.SleepPulseTicks)+1)

	if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 42 {
		t.Fatalf("取消后不应继续治疗: %d", hp)
	}
	if hunger := ecs.Get[components.Hunger](wa.sim, player).Level; hunger != 78 {
		t.Fatalf("取消后不应继续消耗 hunger: %d", hunger)
	}
}

func TestSleepCommitRevalidatesCampfire(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WorldActor, ecs.Entity, ecs.Entity)
	}{
		{
			name: "demolished",
			mutate: func(wa *WorldActor, _ ecs.Entity, campfire ecs.Entity) {
				DemolishBuilding(wa.sim, campfire)
			},
		},
		{
			name: "out_of_range",
			mutate: func(wa *WorldActor, player, _ ecs.Entity) {
				ecs.Set(wa.sim, player, components.Position{X: systems.SleepRange + 1, Y: 0})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			wa := NewWorldActor(WorldConfig{})
			player := wa.createPlayer("u1")
			campfire := addSleepCampfire(wa, 0, 1, true)
			setSleepVitals(wa, player, 40, 100, 80, 0)
			startSleep(wa, player, 85)
			drainActionOutcomes(wa)
			runActionTicks(wa, int(systems.SleepPulseTicks))
			if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 42 {
				t.Fatalf("第一周期 health=%d, want 42", hp)
			}
			if hunger := ecs.Get[components.Hunger](wa.sim, player).Level; hunger != 78 {
				t.Fatalf("第一周期 hunger=%d, want 78", hunger)
			}
			runActionTicks(wa, int(systems.SleepPulseTicks)-1)
			test.mutate(wa, player, campfire)
			tickWorld(wa)

			if hp := ecs.Get[components.Health](wa.sim, player).Cur; hp != 42 {
				t.Fatalf("第二周期校验失败不应治疗: %d", hp)
			}
			if hunger := ecs.Get[components.Hunger](wa.sim, player).Level; hunger != 78 {
				t.Fatalf("第二周期校验失败不应耗 hunger: %d", hunger)
			}
			requireSingleOutcome(
				t, wa,
				game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED,
				game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET,
			)
		})
	}
}

func TestSleepJournalDecodeAndReplay(t *testing.T) {
	sleepRaw, err := json.Marshal(SleepData{Player: 2})
	if err != nil {
		t.Fatal(err)
	}
	entries := []JournalEntry{
		{Tick: 0, UID: "u1", Kind: JournalJoin},
		{Tick: 0, UID: "u1", Seq: 1, RequestID: 96, Kind: CommandSleep, Data: sleepRaw},
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []JournalEntry
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if data, ok := decoded[1].decodeData().(SleepData); !ok || data.Player != 2 {
		t.Fatalf("sleep journal decode=%#v", decoded[1].decodeData())
	}

	replay := NewWorldActor(WorldConfig{})
	addSleepCampfire(replay, 0, 1, true) // 与日志生成时相同的确定性世界种子。
	replay.Replay(decoded, systems.SleepPulseTicks*2+1)
	player, ok := replay.findPlayer("u1")
	if !ok {
		t.Fatal("replay 未创建玩家")
	}
	if !ecs.Has[components.ActionState](replay.sim, player) {
		t.Fatal("replay 后持续睡眠 ActionState 应保留")
	}
	if hp := ecs.Get[components.Health](replay.sim, player); hp.Cur != hp.Max {
		t.Fatalf("replay health=%d/%d", hp.Cur, hp.Max)
	}
	if hunger := ecs.Get[components.Hunger](replay.sim, player).Level; hunger != 96 {
		t.Fatalf("replay hunger=%d, want 96", hunger)
	}
	state := ecs.Get[components.ActionState](replay.sim, player)
	if state.ActionID == 0 || state.RequestID != 96 ||
		state.CommitTick-state.PhaseStartTick != systems.SleepPulseTicks {
		t.Fatalf("replay 持续状态不确定: %+v", *state)
	}
}
