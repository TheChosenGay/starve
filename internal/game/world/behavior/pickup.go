package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// PickupBehavior 拾取：Looter ↔ Lootable。
// 前置校验（能力/距离/还有东西可捡）在此完成；物品入包 + 实体销毁是命令层副作用
// （堆叠上限来自模板、实体生命周期属世界层，与 Pick 的"产物入包"同一分工）。
type PickupBehavior struct{}

func (PickupBehavior) Do(w *ecs.World, actor, target ecs.Entity) bool {
	_, l := interactive.ActorCap[interactive.Looter](w, actor)
	if l == nil || !ecs.Has[components.Lootable](w, target) || !w.IsAlive(target) {
		return false
	}
	if !withinRange(w, actor, target, l.Range) {
		return false
	}
	return len(ecs.Get[components.Lootable](w, target).Items) > 0
}
