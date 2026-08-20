package components

import (
	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// ActionMetricStage 是低基数动作生命周期阶段。
type ActionMetricStage uint8

const (
	ActionMetricStarted ActionMetricStage = iota + 1
	ActionMetricCommitted
	ActionMetricCompleted
	ActionMetricCanceled
	ActionMetricRejected
)

// ActionMetricEvent 只含有界枚举，不携带实体、请求或动作 ID。
type ActionMetricEvent struct {
	Stage  ActionMetricStage
	Kind   ActionKind
	Reason game.ActionOutcomeReason
}

// ActionMetrics 是 ECS Resource 中的单 tick 观测缓冲。
type ActionMetrics struct {
	Events []ActionMetricEvent
}

func RecordActionMetric(
	w *ecs.World,
	stage ActionMetricStage,
	kind ActionKind,
	reason game.ActionOutcomeReason,
) {
	metrics, ok := ecs.TryResource[ActionMetrics](w)
	if !ok {
		return
	}
	metrics.Events = append(metrics.Events, ActionMetricEvent{
		Stage: stage, Kind: kind, Reason: reason,
	})
}

func DrainActionMetrics(w *ecs.World) []ActionMetricEvent {
	metrics, ok := ecs.TryResource[ActionMetrics](w)
	if !ok {
		return nil
	}
	out := append([]ActionMetricEvent(nil), metrics.Events...)
	metrics.Events = metrics.Events[:0]
	return out
}
