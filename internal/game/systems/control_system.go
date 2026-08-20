package systems

import (
	"sort"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// ControlIntentKind 是 ControlSystem 可仲裁的控制类别。
type ControlIntentKind uint8

const (
	ControlMove ControlIntentKind = iota + 1
	ControlStartAction
	ControlCancelAction
)

// ControlRejectReason 是有界业务拒绝原因。
type ControlRejectReason uint8

const (
	ControlRejectedNone ControlRejectReason = iota
	ControlRejectedInvalidActor
	ControlRejectedBusy
	ControlRejectedInvalidTarget
	ControlRejectedUnsupportedAction
)

// ControlIntent 是单 tick 瞬时控制意图。队列顺序就是仲裁顺序。
type ControlIntent struct {
	Kind       ControlIntentKind
	Actor      ecs.Entity
	ActionKind components.ActionKind
	Target     ecs.Entity
	RequestID  uint64
	Duration   int64
	DX, DY     int
	Path       []components.MoveDir
}

// ControlResult 记录本 tick 的接纳结果，供测试、指标或上层适配器读取。
type ControlResult struct {
	Intent   ControlIntent
	Accepted bool
	Reason   ControlRejectReason
}

// ControlQueue 是世界级 ECS Resource；Intents 每 tick 由 ControlSystem 消费。
type ControlQueue struct {
	Intents      []ControlIntent
	Results      []ControlResult
	nextActionID uint64
}

func EnqueueControl(w *ecs.World, intent ControlIntent) {
	q := ecs.Resource[ControlQueue](w)
	if len(intent.Path) > 0 {
		intent.Path = append([]components.MoveDir(nil), intent.Path...)
	}
	q.Intents = append(q.Intents, intent)
}

func MoveIntent(actor ecs.Entity, dx, dy int) ControlIntent {
	return ControlIntent{Kind: ControlMove, Actor: actor, DX: dx, DY: dy}
}

func PathIntent(actor ecs.Entity, path []components.MoveDir) ControlIntent {
	return ControlIntent{Kind: ControlMove, Actor: actor, Path: path}
}

func StartActionIntent(actor ecs.Entity, kind components.ActionKind, target ecs.Entity, requestID uint64) ControlIntent {
	return ControlIntent{
		Kind:       ControlStartAction,
		Actor:      actor,
		ActionKind: kind,
		Target:     target,
		RequestID:  requestID,
	}
}

func StartCraftIntent(actor ecs.Entity, durationTicks int) ControlIntent {
	return ControlIntent{
		Kind:       ControlStartAction,
		Actor:      actor,
		ActionKind: components.ActionCraft,
		Duration:   int64(durationTicks),
	}
}

// HasPendingAction 判断实体是否已有尚未仲裁的动作开始意图。
func HasPendingAction(w *ecs.World, actor ecs.Entity) bool {
	for _, intent := range ecs.Resource[ControlQueue](w).Intents {
		if intent.Actor == actor && intent.Kind == ControlStartAction {
			return true
		}
	}
	return false
}

// ControlSystem 按队列到达顺序仲裁 Move / StartAction / Cancel。
type ControlSystem struct{}

func (s *ControlSystem) Update(w *ecs.World, dt time.Duration) {
	q := ecs.Resource[ControlQueue](w)
	q.Results = q.Results[:0]
	for _, intent := range q.Intents {
		result := ControlResult{Intent: intent}
		switch intent.Kind {
		case ControlMove:
			result.Accepted, result.Reason = acceptMove(w, intent)
		case ControlStartAction:
			result.Accepted, result.Reason = acceptAction(w, q, intent)
		case ControlCancelAction:
			result.Accepted, result.Reason = acceptCancel(w, intent)
		default:
			result.Reason = ControlRejectedUnsupportedAction
		}
		q.Results = append(q.Results, result)
	}
	q.Intents = q.Intents[:0]
}

func acceptMove(w *ecs.World, intent ControlIntent) (bool, ControlRejectReason) {
	if !w.IsAlive(intent.Actor) || !ecs.Has[components.Position](w, intent.Actor) {
		return false, ControlRejectedInvalidActor
	}
	moving := len(intent.Path) > 0 || intent.DX != 0 || intent.DY != 0
	if moving {
		components.TryInterrupt(w, intent.Actor, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_MOVED)
	}
	mv := ecs.Ensure[components.Moveable](w, intent.Actor)
	if mv.Speed <= 0 {
		mv.Speed = 10
	}
	if mv.EffectiveSpeed <= 0 {
		mv.EffectiveSpeed = mv.Speed
	}
	if len(intent.Path) > 0 {
		mv.DirX, mv.DirY = 0, 0
		mv.Path = append(mv.Path[:0], intent.Path...)
	} else {
		mv.Path = nil
		mv.DirX, mv.DirY = clampControlDir(intent.DX), clampControlDir(intent.DY)
	}
	ecs.MarkDirty[components.Moveable](w, intent.Actor)
	return true, ControlRejectedNone
}

func acceptAction(w *ecs.World, q *ControlQueue, intent ControlIntent) (bool, ControlRejectReason) {
	q.nextActionID++
	actionID := q.nextActionID
	reject := func(controlReason ControlRejectReason, outcomeReason game.ActionOutcomeReason) (bool, ControlRejectReason) {
		components.EmitActionOutcome(w, intent.Actor, components.ActionState{
			ActionID: actionID, Kind: intent.ActionKind, TargetEntity: intent.Target, RequestID: intent.RequestID,
		}, game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_REJECTED, outcomeReason)
		return false, controlReason
	}
	if !w.IsAlive(intent.Actor) || ecs.Has[components.Dead](w, intent.Actor) ||
		ecs.Has[components.Offline](w, intent.Actor) {
		return reject(ControlRejectedInvalidActor, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_ACTOR)
	}
	if ecs.Has[components.ActionState](w, intent.Actor) {
		return reject(ControlRejectedBusy, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_BUSY)
	}
	executors := ecs.Resource[ActionExecutorRegistry](w)
	executor, ok := executors.Resolve(intent.ActionKind)
	if !ok {
		return reject(ControlRejectedUnsupportedAction, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSUPPORTED)
	}
	timing, ok := executor.Timing(intent.Duration)
	if !ok {
		return reject(ControlRejectedUnsupportedAction, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSUPPORTED)
	}
	if reason := executor.Validate(w, intent.Actor, intent.Target); reason != ControlRejectedNone {
		return reject(reason, outcomeReasonForRejection(reason))
	}

	stopMoving(w, intent.Actor)
	now := int64(worldPhase(w))
	ecs.Add(w, intent.Actor, components.ActionState{
		ActionID:       actionID,
		Kind:           intent.ActionKind,
		TargetEntity:   intent.Target,
		RequestID:      intent.RequestID,
		Phase:          components.ActionWindup,
		PhaseStartTick: now,
		PhaseEndTick:   now + timing.Windup,
		CommitTick:     now + timing.Windup,
		EndTick:        now + timing.Windup + timing.Recovery,
	})
	components.RecordActionMetric(w, components.ActionMetricStarted, intent.ActionKind, 0)
	if ecs.Has[components.AI](w, intent.Actor) {
		ai := ecs.Get[components.AI](w, intent.Actor)
		if intent.ActionKind == components.ActionAttack {
			ai.Cooldown = weaponOf(w, intent.Actor).AttackCooldown
		}
		ecs.MarkDirty[components.AI](w, intent.Actor)
	}
	return true, ControlRejectedNone
}

func acceptCancel(w *ecs.World, intent ControlIntent) (bool, ControlRejectReason) {
	if !w.IsAlive(intent.Actor) {
		return false, ControlRejectedInvalidActor
	}
	if !ecs.Has[components.ActionState](w, intent.Actor) && !ecs.Has[components.Crafting](w, intent.Actor) {
		return false, ControlRejectedBusy
	}
	components.TryInterrupt(w, intent.Actor, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT)
	return true, ControlRejectedNone
}

func stopMoving(w *ecs.World, actor ecs.Entity) {
	if !ecs.Has[components.Moveable](w, actor) {
		return
	}
	mv := ecs.Get[components.Moveable](w, actor)
	if mv.DirX == 0 && mv.DirY == 0 && len(mv.Path) == 0 {
		return
	}
	mv.DirX, mv.DirY = 0, 0
	mv.Path = nil
	ecs.MarkDirty[components.Moveable](w, actor)
}

func clampControlDir(v int) int {
	if v > 0 {
		return 1
	}
	if v < 0 {
		return -1
	}
	return 0
}

type ActionTiming struct {
	Windup, Recovery int64
}

func outcomeReasonForRejection(reason ControlRejectReason) game.ActionOutcomeReason {
	switch reason {
	case ControlRejectedInvalidActor:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_ACTOR
	case ControlRejectedBusy:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_BUSY
	case ControlRejectedInvalidTarget:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET
	case ControlRejectedUnsupportedAction:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSUPPORTED
	default:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSPECIFIED
	}
}

// ActionCommit 是已成功提交、需要世界适配层补齐副作用的语义记录。
type ActionCommit struct {
	Actor  ecs.Entity
	Target ecs.Entity
	Kind   components.ActionKind
}

type ActionCommitQueue struct {
	Commits []ActionCommit
}

func DrainActionCommits(w *ecs.World) []ActionCommit {
	q := ecs.Resource[ActionCommitQueue](w)
	out := append([]ActionCommit(nil), q.Commits...)
	q.Commits = q.Commits[:0]
	return out
}

// ActionSystem 推进 phase，并对同 tick due actions 先冻结集合再统一 commit。
type ActionSystem struct{}

type dueAction struct {
	actor ecs.Entity
	state components.ActionState
}

func (s *ActionSystem) Update(w *ecs.World, dt time.Duration) {
	now := int64(worldPhase(w))
	due := make([]dueAction, 0)
	ecs.Query[components.ActionState](w, func(e ecs.Entity, state *components.ActionState) {
		if state.Phase == components.ActionWindup && now >= state.CommitTick {
			due = append(due, dueAction{actor: e, state: *state})
		}
	})
	sort.Slice(due, func(i, j int) bool {
		if due[i].state.ActionID == due[j].state.ActionID {
			return due[i].actor < due[j].actor
		}
		return due[i].state.ActionID < due[j].state.ActionID
	})

	results := make(map[uint64]ActionCommitResult, len(due))
	executors := ecs.Resource[ActionExecutorRegistry](w)
	// due 集合已经冻结；结算过程中产生的血量变化不会改变本批候选集合。
	for _, action := range due {
		executor, ok := executors.Resolve(action.state.Kind)
		if !ok {
			continue
		}
		result := executor.Commit(w, action.actor, action.state)
		results[action.state.ActionID] = result
		if result.Committed {
			components.RecordActionMetric(w, components.ActionMetricCommitted, action.state.Kind, 0)
		}
	}
	for _, action := range due {
		if !w.IsAlive(action.actor) || !ecs.Has[components.ActionState](w, action.actor) {
			continue
		}
		state := ecs.Get[components.ActionState](w, action.actor)
		if state.ActionID != action.state.ActionID || state.Phase != components.ActionWindup {
			continue
		}
		if results[state.ActionID].CompleteImmediately {
			components.CompleteAction(w, action.actor)
			continue
		}
		state.Phase = components.ActionRecovery
		state.PhaseStartTick = now
		state.PhaseEndTick = state.EndTick
		ecs.MarkDirty[components.ActionState](w, action.actor)
	}

	var completed []ecs.Entity
	ecs.Query[components.ActionState](w, func(e ecs.Entity, state *components.ActionState) {
		if state.Phase == components.ActionRecovery && now >= state.EndTick {
			completed = append(completed, e)
		}
	})
	sort.Slice(completed, func(i, j int) bool { return completed[i] < completed[j] })
	for _, e := range completed {
		if w.IsAlive(e) && ecs.Has[components.ActionState](w, e) {
			components.CompleteAction(w, e)
		}
	}
}
