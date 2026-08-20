package components

import (
	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// TickEventBuffer 持有单 tick 瞬时领域事件；NextEventID 跨 tick 单调递增。
type TickEventBuffer struct {
	NextEventID uint64
	Tick        int64
	Events      []*game.WorldEvent
}

func BeginTickEvents(w *ecs.World, tick int64) {
	if buffer, ok := ecs.TryResource[TickEventBuffer](w); ok {
		buffer.Tick = tick
	}
}

func EmitCombatImpact(
	w *ecs.World,
	source, target ecs.Entity,
	actionID uint64,
	result game.CombatImpactResult,
) {
	emitWorldEvent(w, &game.WorldEvent{Payload: &game.WorldEvent_Impact{
		Impact: &game.CombatImpactEvent{
			SourceEntity:   uint64(source),
			TargetEntity:   uint64(target),
			SourceActionId: actionID,
			Result:         result,
		},
	}})
}

func EmitActionOutcomeEvent(w *ecs.World, outcome *game.ActionOutcome) {
	if outcome == nil {
		return
	}
	if buffer, ok := ecs.TryResource[TickEventBuffer](w); ok {
		outcome.Tick = buffer.Tick
	}
	emitWorldEvent(w, &game.WorldEvent{Payload: &game.WorldEvent_Outcome{
		Outcome: outcome,
	}})
}

func EmitHealthChanged(
	w *ecs.World,
	target, source ecs.Entity,
	delta int,
	cause game.HealthChangeCause,
	actionID uint64,
) {
	if delta == 0 {
		return
	}
	emitWorldEvent(w, &game.WorldEvent{Payload: &game.WorldEvent_HealthChanged{
		HealthChanged: &game.HealthChangedEvent{
			TargetEntity:   uint64(target),
			SourceEntity:   uint64(source),
			Delta:          int32(delta),
			Cause:          cause,
			SourceActionId: actionID,
		},
	}})
}

// ApplyHealthDelta 是非攻击来源的统一 Health 变更入口，并按实际变化生成事件。
func ApplyHealthDelta(
	w *ecs.World,
	target, source ecs.Entity,
	delta int,
	cause game.HealthChangeCause,
	actionID uint64,
) int {
	if delta == 0 || !w.IsAlive(target) || !ecs.Has[Health](w, target) {
		return 0
	}
	health := ecs.Get[Health](w, target)
	before := health.Cur
	health.Cur += delta
	if health.Cur < 0 {
		health.Cur = 0
	}
	if health.Max > 0 && health.Cur > health.Max {
		health.Cur = health.Max
	}
	applied := health.Cur - before
	if applied == 0 {
		return 0
	}
	ecs.MarkDirty[Health](w, target)
	EmitHealthChanged(w, target, source, applied, cause, actionID)
	return applied
}

func DrainTickEvents(w *ecs.World) []*game.WorldEvent {
	buffer, ok := ecs.TryResource[TickEventBuffer](w)
	if !ok {
		return nil
	}
	out := append([]*game.WorldEvent(nil), buffer.Events...)
	buffer.Events = buffer.Events[:0]
	return out
}

func emitWorldEvent(w *ecs.World, event *game.WorldEvent) {
	buffer, ok := ecs.TryResource[TickEventBuffer](w)
	if !ok {
		return
	}
	buffer.NextEventID++
	event.EventId = buffer.NextEventID
	event.Tick = buffer.Tick
	buffer.Events = append(buffer.Events, event)
}
