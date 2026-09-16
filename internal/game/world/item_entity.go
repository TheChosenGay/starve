package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 本文件放"物品 ←→ 世界实体"的转换辅助。
//
// 为什么需要它：投掷的被投物必须是一个**世界实体**（要有 Position 才能在
// 抛物线里飞），而玩家的炸弹在**背包**里（只是一个 ItemStack）。
// 投掷时要把背包里的一个炸弹"实体化"到玩家手里，才能进入投掷流程。
//
// 这与 Drop（丢弃）是同一件事的前半段：丢弃 = 实体化到脚下，
// 投掷 = 实体化到手里再抛出去。这里抽出共用逻辑，避免两处各写一遍。

// SpawnItemEntity 在 (x,y) 生成一个物品实体（Lootable + 模板驱动的组件）。
//
// 会按物品模板挂上可投掷属性（Throwable）——这样"能不能扔"与
// "能扔多远"完全由配置决定，加新可投掷物只需改 JSON。
func (a *WorldActor) SpawnItemEntity(x, y int, stack components.ItemStack) ecs.Entity {
	e := a.sim.CreateEntity()
	ecs.Add(a.sim, e, components.Position{X: x, Y: y})
	ecs.Add(a.sim, e, components.Lootable{Items: []components.ItemStack{stack}})
	a.attachItemComponents(e, stack.Kind)
	return e
}

// attachItemComponents 按模板给物品实体挂组件（当前只有可投掷）。
func (a *WorldActor) attachItemComponents(e ecs.Entity, kind components.ItemKind) {
	tpl, ok := a.config.Templates[kind]
	if !ok {
		return
	}
	if tpl.Throw != nil && tpl.Throw.Mass > 0 {
		ecs.Add(a.sim, e, components.Throwable{Mass: tpl.Throw.Mass})
	}
}

// materializeOneForThrow 从玩家背包取出一个 kind，实体化到玩家所在格，
// 返回该实体（0 = 背包里没有 / 无法实体化）。
//
// 语义：投掷要求被投物是**世界实体**且"在手里"（与投掷者同格或相邻）。
// 这里直接放在玩家所在格，天然满足"在手里"的距离校验。
func (a *WorldActor) materializeOneForThrow(player ecs.Entity, kind components.ItemKind) ecs.Entity {
	if !ecs.Has[components.Inventory](a.sim, player) ||
		!ecs.Has[components.Position](a.sim, player) {
		return 0
	}
	inv := ecs.Get[components.Inventory](a.sim, player)
	if !inv.Take(kind, 1) {
		return 0
	}
	ecs.MarkDirty[components.Inventory](a.sim, player)
	pos := ecs.Get[components.Position](a.sim, player)
	return a.SpawnItemEntity(pos.X, pos.Y, components.ItemStack{Kind: kind, Count: 1})
}
