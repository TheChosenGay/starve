package worldmap

import "starve/internal/game/components"

// ResourceSeed 资源配置表里的一条种子实体（JSON 原始形态）。
type ResourceSeed struct {
	Kind   string `json:"kind"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Action string `json:"action"` // chop/mine/pick（Workable 动作）
	Work   int    `json:"work"`   // 剩余工作量（耐久）
}

// StationSeed 工作站配置：类型（配置名）+ 坐标。
type StationSeed struct {
	Type string `json:"type"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

// SeededResource 校验后的种子实体：kind 已解析为枚举（生成器产出/存档用）。
type SeededResource struct {
	Kind   components.ItemKind
	X, Y   int
	Action components.WorkAction
	Work   int
}
