package components

import (
	"starve/internal/ecs"
)

// 本文件是 **Boss 动作事件**：行为树把"投弹/闪现/嚎叫/锤地"表达成事件，
// 由世界层（systems 或演示层）消费并产生实际效果（生成炸弹实体、结算 AOE 伤害…）。
//
// 为什么不直接在行为树的 Env 里改世界：
//   - 行为树是**纯决策层**，保持"产出意图、不产生副作用"才能单测与重放；
//   - 与项目既有分工一致（ControlSystem 仲裁控制意图、ActionSystem 结算伤害）。
//
// 事件存在世界级 Resource 里，每 tick 由消费者取走（Drain 语义），
// 与 TickEventBuffer 的做法一致。

// BossActionKind Boss 动作类别。
type BossActionKind uint8

const (
	BossActionThrowBomb BossActionKind = iota + 1 // 投掷炸弹
	BossActionLeap                                // 闪现位移
	BossActionRoar                                // 嚎叫
	BossActionSlam                                // 锤地 AOE
)

// String 便于日志/调试可读。
func (k BossActionKind) String() string {
	switch k {
	case BossActionThrowBomb:
		return "throw_bomb"
	case BossActionLeap:
		return "leap"
	case BossActionRoar:
		return "roar"
	case BossActionSlam:
		return "slam"
	}
	return "unknown"
}

// BossAction 是一次 Boss 动作意图。
type BossAction struct {
	Kind   BossActionKind
	Actor  ecs.Entity // 发起者（Boss）
	Target ecs.Entity // 目标（投弹/闪现时有意义，0 = 无）
	Radius int        // AOE 半径（仅 Slam 有意义）
	Tick   int        // 产生时的世界 tick（供表现层做时序）
}

// BossActionQueue 是世界级 Resource：本 tick 待消费的 Boss 动作。
type BossActionQueue struct {
	Actions []BossAction
}

// EmitBossAction 记录一次 Boss 动作意图（由行为树的 Env 调用）。
func EmitBossAction(w *ecs.World, kind BossActionKind, actor, target ecs.Entity, radius int) {
	q, ok := ecs.TryResource[BossActionQueue](w)
	if !ok {
		return
	}
	tick := 0
	if dc, ok := ecs.TryResource[DayCycle](w); ok {
		tick = dc.Phase
	}
	q.Actions = append(q.Actions, BossAction{
		Kind: kind, Actor: actor, Target: target, Radius: radius, Tick: tick,
	})
}

// DrainBossActions 取走并清空本 tick 的 Boss 动作（保持产生顺序，确定性）。
func DrainBossActions(w *ecs.World) []BossAction {
	q, ok := ecs.TryResource[BossActionQueue](w)
	if !ok {
		return nil
	}
	out := q.Actions
	q.Actions = nil
	return out
}
