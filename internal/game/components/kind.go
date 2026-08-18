package components

import game "starve/pkg/proto/game"

// ItemKind 物品/实体类型（单一事实来源 = proto 枚举）。
// 资源（berry/wood/flint）与工具（axe/pickaxe）共用：类型 + 模板表决定一切静态属性。
type ItemKind = game.ItemKind

// 常用类型常量（配置表/命令里用这些名字）。
const (
	ItemBerry     = game.ItemKind_ITEM_KIND_BERRY
	ItemWood      = game.ItemKind_ITEM_KIND_WOOD
	ItemFlint     = game.ItemKind_ITEM_KIND_FLINT
	ItemMeat      = game.ItemKind_ITEM_KIND_MEAT
	ItemAxe       = game.ItemKind_ITEM_KIND_AXE
	ItemPickaxe   = game.ItemKind_ITEM_KIND_PICKAXE
	ItemWoodArmor = game.ItemKind_ITEM_KIND_WOOD_ARMOR
	ItemHelmet    = game.ItemKind_ITEM_KIND_HELMET
)

// ItemKindByName 配置字符串 → 物品枚举（新资源 = 枚举值 + 这里加一行 + 模板表）。
var ItemKindByName = map[string]ItemKind{
	"berry":      ItemBerry,
	"wood":       ItemWood,
	"flint":      ItemFlint,
	"meat":       ItemMeat,
	"axe":        ItemAxe,
	"pickaxe":    ItemPickaxe,
	"wood_armor": ItemWoodArmor,
	"helmet":     ItemHelmet,
}
