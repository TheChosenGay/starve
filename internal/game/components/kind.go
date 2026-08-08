package components

import game "starve/pkg/proto/game"

// ResourceKind 资源/物品类型（单一事实来源 = proto 枚举）。
// 新资源：加枚举值 + 模板表条目（样式/颜色/掉落等静态属性），游戏代码用常量名。
type ResourceKind = game.ResourceKind

// 常用资源/物品常量（配置表/命令里用这些名字）。
const (
	ResourceBerry   = game.ResourceKind_RESOURCE_KIND_BERRY
	ResourceWood    = game.ResourceKind_RESOURCE_KIND_WOOD
	ResourceFlint   = game.ResourceKind_RESOURCE_KIND_FLINT
	ResourceAxe     = game.ResourceKind_RESOURCE_KIND_AXE
	ResourcePickaxe = game.ResourceKind_RESOURCE_KIND_PICKAXE
)
