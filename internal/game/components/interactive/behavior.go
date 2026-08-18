package interactive

import (
	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Intent 交互意图（复用 WorkAction：chop/mine/pick/attack）。
type Intent = game.WorkAction

const (
	IntentChop   = game.WorkAction_WORK_ACTION_CHOP
	IntentMine   = game.WorkAction_WORK_ACTION_MINE
	IntentPick   = game.WorkAction_WORK_ACTION_PICK
	IntentAttack = game.WorkAction_WORK_ACTION_ATTACK
	IntentPickup = game.WorkAction_WORK_ACTION_PICKUP
)

// Behavior 行为：作用者对被作用者执行一次交互。
// 前置条件（actor 有 -er、target 有 -able）、作用范围全部由行为自己校验；
// 作用效果通过调用 -er 的 Use / -able 的 BeX 组件方法完成，组件自己处理状态变更。
// Do 返回是否真正执行（前置条件不满足 = false）。
type Behavior interface {
	Do(w *ecs.World, actor, target ecs.Entity) bool
}

var behaviors = map[Intent]Behavior{}

// Register 注册一个行为（新交互 = 写一个 Behavior 注册，不改分发器）。
func Register(intent Intent, b Behavior) { behaviors[intent] = b }

// Do 按意图查找行为并执行；未注册或行为前置条件不满足返回 false。
func Do(w *ecs.World, actor, target ecs.Entity, intent Intent) bool {
	b, ok := behaviors[intent]
	if !ok {
		return false
	}
	return b.Do(w, actor, target)
}

// ActorCap 解析作用者的主动能力（-er，实现 Activer），返回（来源实体, 能力）：
// Equip.Hand 有装备实体且带 A → 用装备的（来源 = 装备实体）；否则用作用者自身。
func ActorCap[A Activer](w *ecs.World, actor ecs.Entity) (ecs.Entity, *A) {
	if tool := handTool(w, actor); tool != 0 && ecs.Has[A](w, tool) {
		return tool, ecs.Get[A](w, tool)
	}
	if ecs.Has[A](w, actor) {
		return actor, ecs.Get[A](w, actor)
	}
	return 0, nil
}

// handTool 作用方手持槽位的装备实体（0 = 空手）。
func handTool(w *ecs.World, actor ecs.Entity) ecs.Entity {
	if !ecs.Has[Equip](w, actor) {
		return 0
	}
	return ecs.Get[Equip](w, actor).Item(SlotHand)
}

// WorkDepleted 目标任一受激工作能力是否耗尽。
func WorkDepleted(w *ecs.World, e ecs.Entity) bool {
	if ecs.Has[Choppable](w, e) {
		return ecs.Get[Choppable](w, e).WorkLeft <= 0
	}
	if ecs.Has[Minable](w, e) {
		return ecs.Get[Minable](w, e).WorkLeft <= 0
	}
	if ecs.Has[Pickable](w, e) {
		return ecs.Get[Pickable](w, e).WorkLeft <= 0
	}
	return false
}

// RestoreWork 恢复目标受激工作能力到 MaxWork（重生到点调用）。
func RestoreWork(w *ecs.World, e ecs.Entity) {
	if ecs.Has[Choppable](w, e) {
		c := ecs.Get[Choppable](w, e)
		c.WorkLeft = c.MaxWork
		ecs.MarkDirty[Choppable](w, e)
	}
	if ecs.Has[Minable](w, e) {
		c := ecs.Get[Minable](w, e)
		c.WorkLeft = c.MaxWork
		ecs.MarkDirty[Minable](w, e)
	}
	if ecs.Has[Pickable](w, e) {
		c := ecs.Get[Pickable](w, e)
		c.WorkLeft = c.MaxWork
		ecs.MarkDirty[Pickable](w, e)
	}
}
