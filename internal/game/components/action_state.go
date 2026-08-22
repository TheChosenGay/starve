package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// ActionKind 是权威持续动作类型。移动由控制系统独立仲裁，不属于动作。
type ActionKind = game.ActionKind

const (
	ActionAttack = game.ActionKind_ACTION_KIND_ATTACK
	ActionChop   = game.ActionKind_ACTION_KIND_CHOP
	ActionMine   = game.ActionKind_ACTION_KIND_MINE
	ActionPick   = game.ActionKind_ACTION_KIND_PICK
	ActionCraft  = game.ActionKind_ACTION_KIND_CRAFT
	ActionSleep  = game.ActionKind_ACTION_KIND_SLEEP
	ActionHaunt  = game.ActionKind_ACTION_KIND_HAUNT
)

// ActionPhase 是动作时间轴阶段；动作完成后移除 ActionState。
type ActionPhase = game.ActionPhase

const (
	ActionWindup   = game.ActionPhase_ACTION_PHASE_WINDUP
	ActionRecovery = game.ActionPhase_ACTION_PHASE_RECOVERY
)

// ActionState 是实体当前唯一的权威持续动作。
type ActionState struct {
	ActionID        uint64
	Kind            ActionKind
	TargetEntity    ecs.Entity
	RequestID       uint64
	Phase           ActionPhase
	PhaseStartTick  int64
	PhaseEndTick    int64
	CommitTick      int64
	EndTick         int64
	Uninterruptible bool
}

func init() { RegisterInterruptable[ActionState]() }

// Resume 实现 Interruptable。ActionState 不持有可退款业务数据，只发出一次取消结果。
func (a *ActionState) Resume(w *ecs.World, e ecs.Entity, reason InterruptReason) {
	EmitActionOutcome(w, e, *a, game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED, reason)
}

// CanInterrupt 让通用 TryInterrupt 尊重动作注册策略。
func (a *ActionState) CanInterrupt() bool { return !a.Uninterruptible }

var _ Interruptable = (*ActionState)(nil)

func EmitActionOutcome(
	w *ecs.World,
	entity ecs.Entity,
	state ActionState,
	result game.ActionOutcomeResult,
	reason game.ActionOutcomeReason,
) {
	EmitActionOutcomeEvent(w, &game.ActionOutcome{
		EntityId:  uint64(entity),
		ActionId:  state.ActionID,
		RequestId: state.RequestID,
		Kind:      state.Kind,
		Result:    result,
		Reason:    reason,
	})
	switch result {
	case game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_COMPLETED:
		RecordActionMetric(w, ActionMetricCompleted, state.Kind, reason)
	case game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED:
		RecordActionMetric(w, ActionMetricCanceled, state.Kind, reason)
	case game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_REJECTED:
		RecordActionMetric(w, ActionMetricRejected, state.Kind, reason)
	}
}

// CompleteAction 原子表达动作完成语义后移除 ActionState。
func CompleteAction(w *ecs.World, entity ecs.Entity) bool {
	if !w.IsAlive(entity) || !ecs.Has[ActionState](w, entity) {
		return false
	}
	state := *ecs.Get[ActionState](w, entity)
	EmitActionOutcome(
		w, entity, state,
		game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_COMPLETED,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSPECIFIED,
	)
	ecs.Remove[ActionState](w, entity)
	return true
}

type actionStateCodec struct{}

func (actionStateCodec) Encode(v ActionState) ([]byte, error) {
	return pb.Marshal(&game.ActionState{
		ActionId:        v.ActionID,
		Kind:            v.Kind,
		TargetEntity:    uint64(v.TargetEntity),
		RequestId:       v.RequestID,
		Phase:           v.Phase,
		PhaseStartTick:  v.PhaseStartTick,
		PhaseEndTick:    v.PhaseEndTick,
		CommitTick:      v.CommitTick,
		EndTick:         v.EndTick,
		Uninterruptible: v.Uninterruptible,
	})
}

func (actionStateCodec) Decode(b []byte) (ActionState, error) {
	var m game.ActionState
	if err := pb.Unmarshal(b, &m); err != nil {
		return ActionState{}, err
	}
	return ActionState{
		ActionID:        m.ActionId,
		Kind:            m.Kind,
		TargetEntity:    ecs.Entity(m.TargetEntity),
		RequestID:       m.RequestId,
		Phase:           m.Phase,
		PhaseStartTick:  m.PhaseStartTick,
		PhaseEndTick:    m.PhaseEndTick,
		CommitTick:      m.CommitTick,
		EndTick:         m.EndTick,
		Uninterruptible: m.Uninterruptible,
	}, nil
}

func RegisterActionState(w *ecs.World) {
	ecs.RegisterComponent(w, "ActionState", actionStateCodec{})
}
