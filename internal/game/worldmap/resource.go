package worldmap

import (
	"fmt"

	"starve/internal/game/components"
)

// ResourceSeed 资源配置表里的一条种子实体（JSON 原始形态）。
type ResourceSeed struct {
	Kind   string `json:"kind"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Action string `json:"action,omitempty"` // chop/mine/pick；空表示仅有资源身份与位置
	Work   int    `json:"work,omitempty"`   // 剩余工作量；无动作时必须为 0
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
	Action components.WorkAction // 0 表示非交互环境物
	Work   int                   // Action=0 时为 0
}

// ResolveResourceSpec 把配置名归一化为资源类型和交互动作。
// 空 action 表示纯环境物，此时 work 必须为 0。
func ResolveResourceSpec(kindName, actionName string, work int) (components.ItemKind, components.WorkAction, error) {
	kind, ok := components.ItemKindByName[kindName]
	if !ok {
		return 0, 0, fmt.Errorf("unknown resource kind %q", kindName)
	}
	if actionName == "" {
		if work != 0 {
			return 0, 0, fmt.Errorf("work must be 0 when action is empty for kind %q", kindName)
		}
		return kind, 0, nil
	}
	action, ok := components.WorkActionByName[actionName]
	if !ok {
		return 0, 0, fmt.Errorf("unknown work action %q for kind %q", actionName, kindName)
	}
	if work <= 0 {
		return 0, 0, fmt.Errorf("work must be > 0 for actionable kind %q", kindName)
	}
	return kind, action, nil
}
