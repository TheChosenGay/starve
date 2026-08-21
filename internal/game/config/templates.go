package config

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/game/components"
)

// DropEntry 保留旧名；配置加载后已归一化为完整 DropRule。
type DropEntry = components.DropRule

// ItemTemplate 一种资源/物品的静态属性模板（配置驱动，加资源 = 加枚举 + 加一行模板）。
// 采集/掉落/使用/客户端样式都从这里取。
type ItemTemplate struct {
	Name         string                `json:"name"`                    // 显示名（客户端）
	Color        string                `json:"color"`                   // 颜色（客户端）
	StackSize    int                   `json:"stack_size"`              // 堆叠上限（默认 20）
	Tool         *ToolSpec             `json:"tool,omitempty"`          // 工具属性（砍/挖效率 + 耐久）
	Armor        *ArmorSpec            `json:"armor,omitempty"`         // 护甲属性（防御减免 + 槽位）
	UseEffect    *UseEffect            `json:"use_effect,omitempty"`    // 使用效果（吃/喝）
	DropTable    []components.DropRule `json:"drop_table,omitempty"`    // 资源耗尽后的默认掉落
	RespawnTicks int                   `json:"respawn_ticks,omitempty"` // 重生间隔（预留）
	Blocking     bool                  `json:"blocking,omitempty"`      // 实体态是否占格（树/岩挡路；物品态无意义）
}

// ToolSpec 工具属性：能做什么动作 + 每次工作减少的工作量 + 总耐久。
// 属性在模板（单一来源），耐久状态在背包物品实例（每次成功工作 -1）。
type ToolSpec struct {
	Action     components.WorkAction `json:"action"`
	Efficiency int                   `json:"efficiency"`
	Durability int                   `json:"durability"`
}

// ArmorSpec 护甲属性：防御减免百分比 + 装备槽位（"head" 头戴 / "body" 身穿）。
type ArmorSpec struct {
	Percent int    `json:"percent"`
	Slot    string `json:"slot"`
}

// UnmarshalJSON 支持配置写字符串动作（"chop"/"mine"/"pick"）。
func (t *ToolSpec) UnmarshalJSON(b []byte) error {
	var raw struct {
		Action     string `json:"action"`
		Efficiency int    `json:"efficiency"`
		Durability int    `json:"durability"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	action, ok := components.WorkActionByName[raw.Action]
	if !ok {
		return fmt.Errorf("unknown work action %q", raw.Action)
	}
	*t = ToolSpec{Action: action, Efficiency: raw.Efficiency, Durability: raw.Durability}
	return nil
}

// UseEffect 使用物品的效果（作用于玩家组件）。
type UseEffect struct {
	Hunger int `json:"hunger"` // 饥饿 +N（正数恢复，负数扣）
	Health int `json:"health"` // 血量 +N
}

// loadTemplates 读取资源模板表（kind → Template），校验并补默认值。
func loadTemplates(path string) (map[components.ItemKind]ItemTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := map[string]ItemTemplate{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[components.ItemKind]ItemTemplate, len(raw))
	for name, t := range raw {
		kind, ok := components.ItemKindByName[name]
		if !ok {
			return nil, fmt.Errorf("unknown template kind %q", name)
		}
		if t.StackSize <= 0 {
			t.StackSize = 20
		}
		out[kind] = t
	}
	return out, nil
}
