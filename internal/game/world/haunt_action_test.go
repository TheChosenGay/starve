package world

import (
	"testing"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	game "starve/pkg/proto/game"
)

func newHauntTestWorld(t *testing.T) *WorldActor {
	t.Helper()
	wa := NewWorldActor(WorldConfig{})
	wa.sim.AddResource(&MapData{Width: 12, Height: 12})
	return wa
}

func addDeadHauntPlayer(wa *WorldActor, uid string, x, y int) ecs.Entity {
	player := wa.createPlayer(uid)
	ecs.Set(wa.sim, player, components.Position{X: x, Y: y})
	health := ecs.Get[components.Health](wa.sim, player)
	health.Cur = 0
	ecs.MarkDirty[components.Health](wa.sim, player)
	ecs.Add(wa.sim, player, components.Dead{Reason: "test", SinceTick: 1})
	return player
}

func addHauntStatue(wa *WorldActor, x, y, uses int, duration int64) ecs.Entity {
	statue := wa.sim.CreateEntity()
	ecs.Add(wa.sim, statue, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, statue, components.Hauntable{
		RemainingUses: uses,
		DurationTicks: duration,
	})
	ecs.Add(wa.sim, statue, components.Block{Width: 1, Height: 1})
	return statue
}

func startHaunt(wa *WorldActor, uid string, player, statue ecs.Entity) {
	wa.cmds.Handle(Command{
		UID: uid, Kind: CommandHaunt,
		Data: HauntData{Player: player, Target: statue},
	})
	tickWorld(wa)
}

func TestActionPolicyDefaultsAndHauntFlags(t *testing.T) {
	registry := systems.NewActionExecutorRegistry()
	_, attackPolicy, ok := registry.ResolveDefinition(components.ActionAttack)
	if !ok || attackPolicy.AllowWhenDead || attackPolicy.Uninterruptible {
		t.Fatalf("默认策略错误: ok=%v policy=%+v", ok, attackPolicy)
	}
	_, hauntPolicy, ok := registry.ResolveDefinition(components.ActionHaunt)
	if !ok || !hauntPolicy.AllowWhenDead || !hauntPolicy.Uninterruptible {
		t.Fatalf("作祟策略错误: ok=%v policy=%+v", ok, hauntPolicy)
	}
}

func TestDeadOnlyStartsHauntAndLivingCannotHaunt(t *testing.T) {
	t.Run("dead_rejects_other_action", func(t *testing.T) {
		wa := newHauntTestWorld(t)
		player := addDeadHauntPlayer(wa, "dead", 4, 4)
		target := addActionTarget(wa, 4, 5, 100)
		wa.cmds.Handle(Command{
			UID: "dead", Kind: CommandAttack,
			Data: AttackData{Attacker: player, Target: target},
		})
		tickWorld(wa)
		if ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("死亡玩家不应启动普通动作")
		}
	})

	t.Run("dead_starts_haunt", func(t *testing.T) {
		wa := newHauntTestWorld(t)
		player := addDeadHauntPlayer(wa, "dead", 4, 4)
		statue := addHauntStatue(wa, 4, 5, 1, 60)
		startHaunt(wa, "dead", player, statue)
		state := ecs.Get[components.ActionState](wa.sim, player)
		if state.Kind != components.ActionHaunt || !state.Uninterruptible {
			t.Fatalf("作祟状态错误: %+v", state)
		}
	})

	t.Run("living_rejected", func(t *testing.T) {
		wa := newHauntTestWorld(t)
		player := wa.createPlayer("living")
		ecs.Set(wa.sim, player, components.Position{X: 4, Y: 4})
		statue := addHauntStatue(wa, 4, 5, 1, 60)
		startHaunt(wa, "living", player, statue)
		if ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("存活玩家不应启动作祟")
		}
	})
}

func TestHauntCannotBeInterruptedOrMutatedByControl(t *testing.T) {
	wa := newHauntTestWorld(t)
	player := addDeadHauntPlayer(wa, "u1", 4, 4)
	statue := addHauntStatue(wa, 4, 5, 1, 60)
	moveable := ecs.Get[components.Moveable](wa.sim, player)
	moveable.DirX, moveable.DirY = 0, 0
	moveable.SubX, moveable.SubY = 0.25, 0.5
	moveable.Path = []components.MoveDir{{DX: 1}}
	ecs.MarkDirty[components.Moveable](wa.sim, player)
	startHaunt(wa, "u1", player, statue)

	initialState := *ecs.Get[components.ActionState](wa.sim, player)
	initialPosition := *ecs.Get[components.Position](wa.sim, player)
	initialMoveable := *ecs.Get[components.Moveable](wa.sim, player)
	initialMoveable.Path = append([]components.MoveDir(nil), initialMoveable.Path...)

	wa.cmds.Handle(Command{
		UID: "u1", Kind: CommandMove,
		Data: MoveData{Entity: player, DX: -1, DY: 1},
	})
	tickWorld(wa)
	results := ecs.Resource[systems.ControlQueue](wa.sim).Results
	if len(results) != 1 || results[0].Reason != systems.ControlRejectedBusy {
		t.Fatalf("Move 拒绝原因=%+v, want Busy", results)
	}
	if got := *ecs.Get[components.ActionState](wa.sim, player); got != initialState {
		t.Fatalf("Move 改变了作祟时间线: got=%+v want=%+v", got, initialState)
	}
	if got := *ecs.Get[components.Position](wa.sim, player); got != initialPosition {
		t.Fatalf("Move 改变了位置: got=%+v want=%+v", got, initialPosition)
	}
	gotMoveable := ecs.Get[components.Moveable](wa.sim, player)
	if gotMoveable.DirX != initialMoveable.DirX || gotMoveable.DirY != initialMoveable.DirY ||
		gotMoveable.SubX != initialMoveable.SubX || gotMoveable.SubY != initialMoveable.SubY ||
		len(gotMoveable.Path) != len(initialMoveable.Path) {
		t.Fatalf("Move 改变了移动状态: got=%+v want=%+v", gotMoveable, initialMoveable)
	}

	wa.cmds.Handle(Command{
		UID: "u1", Kind: CommandCancelAction,
		Data: CancelActionData{Player: player},
	})
	tickWorld(wa)
	results = ecs.Resource[systems.ControlQueue](wa.sim).Results
	if len(results) != 1 || results[0].Reason != systems.ControlRejectedBusy {
		t.Fatalf("CancelAction 拒绝原因=%+v, want Busy", results)
	}
	if got := *ecs.Get[components.ActionState](wa.sim, player); got != initialState {
		t.Fatalf("CancelAction 改变了作祟时间线: got=%+v want=%+v", got, initialState)
	}

	other := addActionTarget(wa, 4, 3, 100)
	wa.cmds.Handle(Command{
		UID: "u1", Kind: CommandAttack,
		Data: AttackData{Attacker: player, Target: other},
	})
	tickWorld(wa)
	results = ecs.Resource[systems.ControlQueue](wa.sim).Results
	if len(results) != 1 || results[0].Reason != systems.ControlRejectedBusy {
		t.Fatalf("新动作拒绝原因=%+v, want Busy", results)
	}
	if got := *ecs.Get[components.ActionState](wa.sim, player); got != initialState {
		t.Fatalf("新动作改变了作祟状态: got=%+v want=%+v", got, initialState)
	}

	for _, reason := range []game.ActionOutcomeReason{
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_MOVED,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DAMAGED,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DEAD,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT,
	} {
		if components.TryInterrupt(wa.sim, player, reason) {
			t.Fatalf("TryInterrupt(%v) 不应中断作祟", reason)
		}
		if !ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatalf("TryInterrupt(%v) 移除了 ActionState", reason)
		}
	}
}

func TestHauntCommitRevivesAndConsumesSingleUseStatue(t *testing.T) {
	wa := newHauntTestWorld(t)
	player := addDeadHauntPlayer(wa, "u1", 5, 4)
	statue := addHauntStatue(wa, 5, 5, 1, 1)
	ecs.Get[components.Effects](wa.sim, player).Active[components.EffectPoison] =
		components.EffectState{Count: 1, Param: 3}
	moveable := ecs.Get[components.Moveable](wa.sim, player)
	moveable.DirX, moveable.DirY = 1, 1
	moveable.SubX, moveable.SubY = 0.4, 0.7
	moveable.Path = []components.MoveDir{{DX: 1}}

	md := ecs.Resource[MapData](wa.sim)
	if md.Walkable(5, 5) {
		t.Fatal("雕像格应先被阻挡")
	}
	startHaunt(wa, "u1", player, statue)
	tickWorld(wa)

	if ecs.Has[components.Dead](wa.sim, player) {
		t.Fatal("完成作祟后玩家仍为 Dead")
	}
	if ecs.Has[components.ActionState](wa.sim, player) {
		t.Fatal("无 recovery 的作祟完成后应移除 ActionState")
	}
	if health := ecs.Get[components.Health](wa.sim, player); health.Cur != health.Max || health.Max <= 0 {
		t.Fatalf("生命未恢复到 Max: %+v", health)
	}
	if hunger := ecs.Get[components.Hunger](wa.sim, player); hunger.Level != 50 {
		t.Fatalf("饥饿值=%d, want 50", hunger.Level)
	}
	if effects := ecs.Get[components.Effects](wa.sim, player); len(effects.Active) != 0 {
		t.Fatalf("效果未清空: %+v", effects.Active)
	}
	moveable = ecs.Get[components.Moveable](wa.sim, player)
	if moveable.DirX != 0 || moveable.DirY != 0 || moveable.SubX != 0 ||
		moveable.SubY != 0 || len(moveable.Path) != 0 {
		t.Fatalf("移动状态未清空: %+v", moveable)
	}
	if position := ecs.Get[components.Position](wa.sim, player); position.X == 5 && position.Y == 5 {
		t.Fatal("复活位置不能与阻挡雕像重叠")
	}
	if wa.sim.IsAlive(statue) {
		t.Fatal("uses=1 的雕像应销毁")
	}
	if !md.Walkable(5, 5) {
		t.Fatal("雕像销毁后应解除地图阻挡")
	}
}

func TestHauntMultiUseAndLastUseCompetition(t *testing.T) {
	t.Run("multi_use_retained", func(t *testing.T) {
		wa := newHauntTestWorld(t)
		player := addDeadHauntPlayer(wa, "u1", 5, 4)
		statue := addHauntStatue(wa, 5, 5, 2, 1)
		startHaunt(wa, "u1", player, statue)
		tickWorld(wa)
		if !wa.sim.IsAlive(statue) {
			t.Fatal("uses>1 的雕像不应销毁")
		}
		if uses := ecs.Get[components.Hauntable](wa.sim, statue).RemainingUses; uses != 1 {
			t.Fatalf("remaining uses=%d, want 1", uses)
		}
	})

	t.Run("lower_entity_wins_last_use", func(t *testing.T) {
		wa := newHauntTestWorld(t)
		lower := addDeadHauntPlayer(wa, "lower", 4, 5)
		higher := addDeadHauntPlayer(wa, "higher", 6, 5)
		statue := addHauntStatue(wa, 5, 5, 1, 1)
		highIntent := systems.StartActionIntent(higher, components.ActionHaunt, statue, 0, 2)
		highIntent.Duration = 1
		lowIntent := systems.StartActionIntent(lower, components.ActionHaunt, statue, 0, 1)
		lowIntent.Duration = 1
		systems.EnqueueControl(wa.sim, highIntent)
		systems.EnqueueControl(wa.sim, lowIntent)
		tickWorld(wa)
		components.DrainTickEvents(wa.sim)
		tickWorld(wa)

		if ecs.Has[components.Dead](wa.sim, lower) || !ecs.Has[components.Dead](wa.sim, higher) {
			t.Fatalf("竞争结果错误: lower dead=%v higher dead=%v",
				ecs.Has[components.Dead](wa.sim, lower), ecs.Has[components.Dead](wa.sim, higher))
		}
		if ecs.Has[components.ActionState](wa.sim, higher) {
			t.Fatal("失败者动作应以 INVALID_TARGET 取消")
		}
		foundCanceledInvalid := false
		for _, event := range components.DrainTickEvents(wa.sim) {
			outcome := event.GetOutcome()
			if outcome != nil && outcome.EntityId == uint64(higher) &&
				outcome.Result == game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED &&
				outcome.Reason == game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET {
				foundCanceledInvalid = true
			}
		}
		if !foundCanceledInvalid {
			t.Fatal("竞争失败者缺少 CANCELED INVALID_TARGET")
		}
	})
}

func TestHauntRejectsRangeAndInvalidatedTarget(t *testing.T) {
	t.Run("out_of_range", func(t *testing.T) {
		wa := newHauntTestWorld(t)
		player := addDeadHauntPlayer(wa, "u1", 1, 1)
		statue := addHauntStatue(wa, 8, 8, 1, 1)
		startHaunt(wa, "u1", player, statue)
		if ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("超距作祟不应启动")
		}
	})

	t.Run("target_invalid_before_commit", func(t *testing.T) {
		wa := newHauntTestWorld(t)
		player := addDeadHauntPlayer(wa, "u1", 5, 4)
		statue := addHauntStatue(wa, 5, 5, 1, 2)
		startHaunt(wa, "u1", player, statue)
		wa.sim.DestroyEntity(statue)
		runActionTicks(wa, 2)
		if !ecs.Has[components.Dead](wa.sim, player) {
			t.Fatal("目标失效后不应复活")
		}
		if ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("目标失效后动作应取消")
		}
	})
}

func TestHauntMigratesV1SaveWithoutDuplicatingExistingStatues(t *testing.T) {
	cfg := WorldConfig{MapPath: "../../../configs/map.json"}
	wa := NewWorldActor(cfg)
	before := 0
	ecs.Query[components.Hauntable](wa.sim, func(_ ecs.Entity, _ *components.Hauntable) {
		before++
	})
	if before == 0 {
		t.Fatal("默认地图应生成复活雕像")
	}

	data := wa.Save()
	var sd game.SaveData
	if err := pb.Unmarshal(data, &sd); err != nil {
		t.Fatal(err)
	}
	sd.Meta.Version = "starve-save-v1"
	patched, err := pb.Marshal(&sd)
	if err != nil {
		t.Fatal(err)
	}

	loaded := NewWorldActor(cfg)
	if err := loaded.Load(patched); err != nil {
		t.Fatal(err)
	}
	after := 0
	ecs.Query[components.Hauntable](loaded.sim, func(_ ecs.Entity, _ *components.Hauntable) {
		after++
	})
	if after != before {
		t.Fatalf("已有雕像的 v1 存档被重复补种: before=%d after=%d", before, after)
	}

	ecs.Query[components.Hauntable](wa.sim, func(e ecs.Entity, _ *components.Hauntable) {
		wa.sim.DestroyEntity(e)
	})
	data = wa.Save()
	if err := pb.Unmarshal(data, &sd); err != nil {
		t.Fatal(err)
	}
	sd.Meta.Version = "starve-save-v1"
	if patched, err = pb.Marshal(&sd); err != nil {
		t.Fatal(err)
	}
	restored := NewWorldActor(cfg)
	if err := restored.Load(patched); err != nil {
		t.Fatal(err)
	}
	migrated := 0
	ecs.Query[components.Hauntable](restored.sim, func(_ ecs.Entity, _ *components.Hauntable) {
		migrated++
	})
	if migrated != before {
		t.Fatalf("无雕像的 v1 存档应补种: got=%d want=%d", migrated, before)
	}
}

func TestHauntSnapshotSaveLoad(t *testing.T) {
	wa := newHauntTestWorld(t)
	player := addDeadHauntPlayer(wa, "u1", 5, 4)
	statue := addHauntStatue(wa, 5, 5, 2, 60)
	startHaunt(wa, "u1", player, statue)

	data := wa.Save()
	loaded := NewWorldActor(WorldConfig{})
	if err := loaded.Load(data); err != nil {
		t.Fatal(err)
	}
	if got := ecs.Get[components.ActionState](loaded.sim, player); !got.Uninterruptible ||
		got.Kind != components.ActionHaunt {
		t.Fatalf("ActionState 未兼容保存: %+v", got)
	}
	if got := ecs.Get[components.Hauntable](loaded.sim, statue); got.RemainingUses != 2 ||
		got.DurationTicks != 60 {
		t.Fatalf("Hauntable 未兼容保存: %+v", got)
	}
	snapshot := FullSnapshot(loaded.sim)
	found := false
	for _, entity := range snapshot.Entities {
		if entity.EntityId != uint64(statue) {
			continue
		}
		for _, state := range entity.Components {
			if state.Component != "Hauntable" {
				continue
			}
			var hauntable game.Hauntable
			if err := pb.Unmarshal(state.Data, &hauntable); err != nil {
				t.Fatal(err)
			}
			found = hauntable.RemainingUses == 2 && hauntable.DurationTicks == 60
		}
	}
	if !found {
		t.Fatal("快照未携带 Hauntable")
	}
}
