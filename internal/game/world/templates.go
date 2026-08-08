package world

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/game/components"
)

// ItemTemplate 一种资源/物品的静态属性模板（配置驱动，加资源 = 加枚举 + 加一行模板）。
// 采集/掉落/使用/客户端样式都从这里取。
type ItemTemplate struct {
	Name         string      `json:"name"`                    // 显示名（客户端）
	Color        string      `json:"color"`                   // 颜色（客户端）
	StackSize    int         `json:"stack_size"`              // 堆叠上限（默认 20）
	UseEffect    *UseEffect  `json:"use_effect,omitempty"`    // 使用效果（吃/喝）
	DropTable    []DropEntry `json:"drop_table,omitempty"`    // 死亡/砍伐掉落
	RespawnTicks int         `json:"respawn_ticks,omitempty"` // 重生间隔（预留）
}

// UseEffect 使用物品的效果（作用于玩家组件）。
type UseEffect struct {
	Hunger int `json:"hunger"` // 饥饿 +N（正数恢复，负数扣）
	Health int `json:"health"` // 血量 +N
}

// DropEntry 掉落表里的一条：kind + 数量。
type DropEntry struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

// loadTemplates 读取资源模板表（kind → Template），校验并补默认值。
func loadTemplates(path string) (map[components.ResourceKind]ItemTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	raw := map[string]ItemTemplate{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[components.ResourceKind]ItemTemplate, len(raw))
	for name, t := range raw {
		kind, ok := resourceKindByName[name]
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

// resolveDropTable 把模板掉落表字符串 kind 解析为枚举（seed 时校验一次）。
func resolveDropTable(table []DropEntry) ([]components.ItemStack, error) {
	out := make([]components.ItemStack, 0, len(table))
	for _, d := range table {
		kind, ok := resourceKindByName[d.Kind]
		if !ok {
			return nil, fmt.Errorf("unknown drop kind %q", d.Kind)
		}
		if d.Count <= 0 {
			continue
		}
		out = append(out, components.ItemStack{Kind: kind, Count: d.Count})
	}
	return out, nil
}
