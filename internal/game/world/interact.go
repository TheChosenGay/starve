package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/world/behavior"
)

// init 注册全部行为（实现分散在 world/behavior 包，各行为一个文件）。
func init() { behavior.Register() }

// handToolOf 作用方手持槽位的装备实体（0 = 空手）。
func handToolOf(w *ecs.World, actor ecs.Entity) ecs.Entity {
	if !ecs.Has[components.Equip](w, actor) {
		return 0
	}
	return ecs.Get[components.Equip](w, actor).Item(components.SlotHand)
}

// brokenTool 手持工具是否耐久耗尽（命令层据此卸下）。
func brokenTool(sim *ecs.World, tool ecs.Entity) bool {
	if ecs.Has[interactive.Chopper](sim, tool) {
		return ecs.Get[interactive.Chopper](sim, tool).Durability <= 0
	}
	if ecs.Has[interactive.Miner](sim, tool) {
		return ecs.Get[interactive.Miner](sim, tool).Durability <= 0
	}
	return false
}
