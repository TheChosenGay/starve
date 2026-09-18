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
	Name      string     `json:"name"`                 // 显示名（客户端）
	Color     string     `json:"color"`                // 颜色（客户端）
	StackSize int        `json:"stack_size"`           // 堆叠上限（默认 20）
	Tool      *ToolSpec  `json:"tool,omitempty"`       // 工具属性（砍/挖效率 + 耐久）
	Armor     *ArmorSpec `json:"armor,omitempty"`      // 护甲属性（防御减免 + 槽位）
	UseEffect *UseEffect `json:"use_effect,omitempty"` // 使用效果（吃/喝）
	// FuelTicks 可燃物能补的**燃烧时长**（tick；20Hz 下 20 tick = 1 秒；0 = 不可燃）。
	// 为什么以"烧多久"为单位而不是"占火堆多少份额"：份额会随火堆上限/消耗速率
	// 一起漂移（改一个数就悄悄改了所有柴的价值），而"这块木头顶 60 秒"是配表
	// 和玩家都能直接对上的常量；燃料耗尽的判定也只需一个减法。
	FuelTicks    int                   `json:"fuel_ticks,omitempty"`
	Throw        *ThrowSpec            `json:"throw,omitempty"`         // 可投掷属性（质量）
	Explode      *ExplodeSpec          `json:"explode,omitempty"`       // 爆炸属性（半径/伤害/击退）
	DropTable    []components.DropRule `json:"drop_table,omitempty"`    // 资源耗尽后的默认掉落
	RespawnTicks int                   `json:"respawn_ticks,omitempty"` // 重生间隔（预留）
	// Blocking 实体态整格阻挡（建筑式占格）；树干/岩石不用它，用 CollisionRadius。
	Blocking bool `json:"blocking,omitempty"`
	// CollisionRadius 实体态碰撞半径（格心圆，单位=格）：挂 components.Block{Radius}，
	// 占位但格子仍可走（寻路代价低），靠近才被形状碰撞挡住并沿切面滑开；0 = 不占位。
	CollisionRadius float64             `json:"collision_radius,omitempty"`
	PickYield       components.ItemKind `json:"-"`                    // 采摘一次的产物；0 = 与实体 kind 相同
	PickYieldRef    string              `json:"pick_yield,omitempty"` // JSON 配置名，加载后归一化到 PickYield
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

// ThrowSpec 可投掷属性。有它 = 这个物品可以被投掷。
//
// 为什么是"有即可能"而不是单独的 bool + mass：投掷的两个语义
// （能不能扔、能扔多远）都来自质量，拆成两个字段会出现
// "可投掷但质量非法"的无意义状态。
type ThrowSpec struct {
	// Mass 质量（正数）。最大投掷距离 = 基础距离 × 投掷者力量 / 质量。
	Mass int `json:"mass"`
}

// ExplodeSpec 爆炸属性。有它 = 这个东西炸开时有威力。
//
// 与 ThrowSpec 正交：可以"能扔但不会炸"（石头），也可以
// "不能扔但会炸"（地雷/炸药桶）。两者都缺就是普通物品。
type ExplodeSpec struct {
	// Radius 爆炸半径（格）。
	Radius float64 `json:"radius"`
	// Damage 对范围内每个目标的伤害。
	Damage int `json:"damage"`
	// Knockback 击退强度（格，0 = 不击退）。
	Knockback float64 `json:"knockback"`
	// FuseTicks 引信时长（0 = 落地立刻炸）。
	FuseTicks int `json:"fuse_ticks"`
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
		if t.Blocking && t.CollisionRadius > 0 {
			return nil, fmt.Errorf("template %q: blocking 与 collision_radius 互斥", name)
		}
		if t.CollisionRadius < 0 || t.CollisionRadius >= 0.5 {
			return nil, fmt.Errorf("template %q: collision_radius 应在 (0, 0.5) 格内，得到 %v", name, t.CollisionRadius)
		}
		if t.PickYieldRef != "" {
			yield, ok := components.ItemKindByName[t.PickYieldRef]
			if !ok {
				return nil, fmt.Errorf("unknown pick yield %q for template %q", t.PickYieldRef, name)
			}
			t.PickYield = yield
		}
		out[kind] = t
	}
	return out, nil
}
