package systems

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	game "starve/pkg/proto/game"
)

// ActionCommitResult 是 executor 向 ActionSystem 返回的稳定提交结果。
type ActionCommitResult struct {
	Committed           bool
	CompleteImmediately bool
	RepeatAfter         int64
	FailureReason       game.ActionOutcomeReason
}

// ActionExecutor 封装各 ActionKind 的校验、时序与权威提交。
type ActionExecutor interface {
	Timing(duration int64) (ActionTiming, bool)
	Validate(w *ecs.World, actor, target ecs.Entity) ControlRejectReason
	Commit(w *ecs.World, actor ecs.Entity, state components.ActionState) ActionCommitResult
}

// ActionExecutorRegistry 是世界级稳定映射；ActionSystem 只依赖本抽象。
type ActionExecutorRegistry struct {
	executors map[components.ActionKind]ActionExecutor
}

func NewActionExecutorRegistry() *ActionExecutorRegistry {
	registry := &ActionExecutorRegistry{executors: make(map[components.ActionKind]ActionExecutor)}
	registry.Register(components.ActionAttack, AttackExecutor{})
	registry.Register(components.ActionChop, workExecutor{intent: interactive.IntentChop})
	registry.Register(components.ActionMine, workExecutor{intent: interactive.IntentMine})
	registry.Register(components.ActionPick, workExecutor{intent: interactive.IntentPick})
	registry.Register(components.ActionCraft, craftExecutor{})
	registry.Register(components.ActionSleep, SleepExecutor{})
	return registry
}

func (r *ActionExecutorRegistry) Register(kind components.ActionKind, executor ActionExecutor) {
	if executor != nil {
		r.executors[kind] = executor
	}
}

func (r *ActionExecutorRegistry) Resolve(kind components.ActionKind) (ActionExecutor, bool) {
	executor, ok := r.executors[kind]
	return executor, ok
}

// AttackExecutor 编排权威伤害结果、动作中断与 tick 领域事件。
type AttackExecutor struct{}

func (AttackExecutor) Timing(int64) (ActionTiming, bool) {
	return ActionTiming{Windup: 8, Recovery: 8}, true
}

func (AttackExecutor) Validate(w *ecs.World, actor, target ecs.Entity) ControlRejectReason {
	if ecs.Has[components.Crafting](w, actor) {
		return ControlRejectedBusy
	}
	if !interactive.CanDo(w, actor, target, interactive.IntentAttack) {
		return ControlRejectedInvalidTarget
	}
	return ControlRejectedNone
}

func (AttackExecutor) Commit(
	w *ecs.World,
	actor ecs.Entity,
	state components.ActionState,
) ActionCommitResult {
	interaction := interactive.Execute(w, actor, state.TargetEntity, interactive.IntentAttack)
	impact := game.CombatImpactResult_COMBAT_IMPACT_RESULT_MISS
	if interaction.Damage != nil && interaction.Damage.Attempted {
		if interaction.Damage.Applied > 0 {
			impact = game.CombatImpactResult_COMBAT_IMPACT_RESULT_HIT
			components.TryInterrupt(
				w, state.TargetEntity,
				game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DAMAGED,
			)
		} else {
			impact = game.CombatImpactResult_COMBAT_IMPACT_RESULT_BLOCKED
		}
	}
	components.EmitCombatImpact(w, actor, state.TargetEntity, state.ActionID, impact)
	if interaction.Damage != nil && interaction.Damage.Applied != 0 {
		components.EmitHealthChanged(
			w, state.TargetEntity, actor,
			-interaction.Damage.Applied,
			game.HealthChangeCause_HEALTH_CHANGE_CAUSE_ATTACK,
			state.ActionID,
		)
	}
	return ActionCommitResult{Committed: interaction.Success}
}

type workExecutor struct {
	intent interactive.Intent
}

func (workExecutor) Timing(int64) (ActionTiming, bool) {
	return ActionTiming{Windup: 4, Recovery: 4}, true
}

func (e workExecutor) Validate(w *ecs.World, actor, target ecs.Entity) ControlRejectReason {
	if ecs.Has[components.Crafting](w, actor) {
		return ControlRejectedBusy
	}
	if !interactive.CanDo(w, actor, target, e.intent) {
		return ControlRejectedInvalidTarget
	}
	return ControlRejectedNone
}

func (e workExecutor) Commit(
	w *ecs.World,
	actor ecs.Entity,
	state components.ActionState,
) ActionCommitResult {
	result := interactive.Execute(w, actor, state.TargetEntity, e.intent)
	if result.Success {
		queue := ecs.Resource[ActionCommitQueue](w)
		queue.Commits = append(queue.Commits, ActionCommit{
			Actor: actor, Target: state.TargetEntity, Kind: state.Kind,
		})
	}
	return ActionCommitResult{Committed: result.Success}
}

type craftExecutor struct{}

func (craftExecutor) Timing(duration int64) (ActionTiming, bool) {
	if duration <= 0 {
		return ActionTiming{}, false
	}
	return ActionTiming{Windup: duration}, true
}

func (craftExecutor) Validate(w *ecs.World, actor, target ecs.Entity) ControlRejectReason {
	return ControlRejectedNone
}

func (craftExecutor) Commit(
	w *ecs.World,
	actor ecs.Entity,
	state components.ActionState,
) ActionCommitResult {
	if !ecs.Has[components.Crafting](w, actor) {
		return ActionCommitResult{CompleteImmediately: true}
	}
	crafting := ecs.Get[components.Crafting](w, actor)
	crafting.TicksLeft = 0
	crafting.Committed = true
	ecs.MarkDirty[components.Crafting](w, actor)
	return ActionCommitResult{Committed: true, CompleteImmediately: true}
}

const (
	SleepPulseTicks = int64(100)
	SleepHealAmount = 2
	SleepHungerCost = 2
	SleepRange      = 3
)

// SleepExecutor 每周期原子结算个人恢复，并请求 ActionSystem 重装同一动作时间轴。
type SleepExecutor struct{}

func (SleepExecutor) Timing(int64) (ActionTiming, bool) {
	return ActionTiming{Windup: SleepPulseTicks}, true
}

func (SleepExecutor) Validate(w *ecs.World, actor, target ecs.Entity) ControlRejectReason {
	if !ecs.Has[components.Position](w, actor) ||
		!ecs.Has[components.Health](w, actor) ||
		!ecs.Has[components.Hunger](w, actor) {
		return ControlRejectedInvalidActor
	}
	if !validSleepTarget(w, actor, target) {
		return ControlRejectedInvalidTarget
	}
	return ControlRejectedNone
}

func (SleepExecutor) Commit(
	w *ecs.World,
	actor ecs.Entity,
	state components.ActionState,
) ActionCommitResult {
	if !validSleepTarget(w, actor, state.TargetEntity) {
		return ActionCommitResult{
			FailureReason: game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET,
		}
	}
	components.ApplyHealthDelta(
		w, actor, actor, SleepHealAmount,
		game.HealthChangeCause_HEALTH_CHANGE_CAUSE_HEALING,
		state.ActionID,
	)
	hunger := ecs.Get[components.Hunger](w, actor)
	hunger.Level -= SleepHungerCost
	if hunger.Level < 0 {
		hunger.Level = 0
	}
	ecs.MarkDirty[components.Hunger](w, actor)
	return ActionCommitResult{Committed: true, RepeatAfter: SleepPulseTicks}
}

func validSleepTarget(w *ecs.World, actor, target ecs.Entity) bool {
	distance, ok := SleepTargetDistance(w, actor, target)
	return ok && distance <= SleepRange
}

// SleepTargetDistance 返回玩家到可睡眠火堆占格的最短距离。
// 玩家建造的 Building 营火和地图预置的 Workstation 营火具有相同玩法语义。
func SleepTargetDistance(w *ecs.World, actor, target ecs.Entity) (int, bool) {
	if !w.IsAlive(target) ||
		!ecs.Has[components.Position](w, target) ||
		!ecs.Has[components.Position](w, actor) {
		return 0, false
	}

	width, height := 1, 1
	switch {
	case ecs.Has[components.Building](w, target):
		building := ecs.Get[components.Building](w, target)
		if !building.Placed || building.Kind != components.BuildingCampfire {
			return 0, false
		}
		width, height = building.Width, building.Height
	case ecs.Has[components.Workstation](w, target):
		if ecs.Get[components.Workstation](w, target).Type != components.StationCampfire {
			return 0, false
		}
		if ecs.Has[components.Block](w, target) {
			block := ecs.Get[components.Block](w, target)
			width, height = block.Width, block.Height
		}
	default:
		return 0, false
	}

	actorPos := ecs.Get[components.Position](w, actor)
	targetPos := ecs.Get[components.Position](w, target)
	return actorPos.ManhattanToFootprint(*targetPos, width, height), true
}
