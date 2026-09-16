package config

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/game/components"
)

// CreatureTemplate 生物模板（creatures.json）：静态属性，生成时拷贝进 Creature 组件。
type CreatureTemplate struct {
	Name             string
	HP               int
	MoveInterval     int // 步进间隔（tick/格）
	PerceptionRadius int // 感知半径（0 = 被动）
	// ThreatRadius 仇恨传播半径（格）：同伴被打时，多远内的同类会被"通知"。
	//
	// 为什么与感知半径分开：感知半径决定"我能看见谁"（要小，否则狼隔着半张图
	// 就发现玩家，失去潜行感）；仇恨传播半径决定"打一只狼，狼群多大范围响应"
	// （要大，否则打了半天只有身边一两只动）。两者语义不同、调参诉求相反，
	// 共用一个值必然顾此失彼——实测狼感知半径只有 6，打一只只有 3/5 同伴响应。
	//
	// 0 = 回退到 PerceptionRadius（保持旧配置行为不变）。
	ThreatRadius   int
	AttackRange    int
	AttackDamage   int
	AttackCooldown int                       // 攻击间隔（tick）
	RoamRadius     int                       // 游荡半径（围绕出生点）
	FleeHPRatio    float32                   // 血量低于该比例切 flee（0 = 永不逃跑）
	HitMemoryTicks int                       // 受击记忆窗口（tick）
	HostileKinds   []components.CreatureKind // 视为敌对的生物类型（玩家隐式敌对）
	HostilePlayers bool                      // 玩家是否视为敌对
	Drops          []components.DropRule
	// BodyRadius/BodyHeight 生物的简化碰撞体（格），由客户端模型推导：
	// go run ./cmd/modelcollide（见 configs/models.json 与 docs/模型到碰撞体流水线.md）。
	// seedCreatures 把它们写进 Moveable，移动时由 systems.BodyOf 按实体取用
	// （只有 BodyRadius = 0 才回退全局缺省 systems.BodyRadius），所以必须显式配置，
	// loadCreatures 会 fail fast。
	BodyRadius float64
	BodyHeight float64
	// BodyHalfLength 四足生物的胶囊半长（格，沿朝向铺开；0 = 直立圆柱）：
	// 长宽刚好包住模型（狼 1.06、鹿 0.89…），来自同一份模型推导。
	BodyHalfLength float64
	// TreeKind 行为树种类（predator/prey/dormant）；留空则按"能否攻击"推断
	// （attack_damage > 0 → predator，否则 prey）。见 internal/game/behavior。
	TreeKind components.BehaviorTreeKind
	// Leash 拴绳半径（格）：目标超出即放弃追击。0 = 用缺省 4+感知半径。
	Leash int
}

type creatureJSON struct {
	Kind             string                `json:"kind"`
	Name             string                `json:"name"`
	HP               int                   `json:"hp"`
	MoveInterval     int                   `json:"move_interval"`
	PerceptionRadius int                   `json:"perception_radius"`
	ThreatRadius     int                   `json:"threat_radius"`
	AttackRange      int                   `json:"attack_range"`
	AttackDamage     int                   `json:"attack_damage"`
	AttackCooldown   int                   `json:"attack_cooldown"`
	RoamRadius       int                   `json:"roam_radius"`
	FleeHPRatio      float32               `json:"flee_hp_ratio"`
	HitMemoryTicks   int                   `json:"hit_memory_ticks"`
	Hostile          []string              `json:"hostile"`
	HostilePlayers   *bool                 `json:"hostile_players"` // 指针：缺省 false（友好）
	Drops            []components.DropRule `json:"drops"`
	BodyRadius       float64               `json:"body_radius"`
	BodyHeight       float64               `json:"body_height"`
	BodyHalfLength   float64               `json:"body_half_length"`
	Tree             string                `json:"behavior_tree"` // 行为树：predator/prey/dormant（留空按攻击力推断）
	Leash            int                   `json:"leash"`         // 拴绳半径（格）；0 = 缺省 4+感知半径
}

// loadCreatures 读取 creatures.json（生物模板表），fail fast。
func loadCreatures(path string) (map[components.CreatureKind]CreatureTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Creatures []creatureJSON `json:"creatures"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[components.CreatureKind]CreatureTemplate, len(raw.Creatures))
	for _, c := range raw.Creatures {
		kind, ok := components.CreatureKindByName[c.Kind]
		if !ok {
			return nil, fmt.Errorf("unknown creature kind %q", c.Kind)
		}
		tpl := CreatureTemplate{
			Name:             c.Name,
			HP:               c.HP,
			MoveInterval:     c.MoveInterval,
			PerceptionRadius: c.PerceptionRadius,
			ThreatRadius:     c.ThreatRadius,
			AttackRange:      c.AttackRange,
			AttackDamage:     c.AttackDamage,
			AttackCooldown:   c.AttackCooldown,
			RoamRadius:       c.RoamRadius,
			FleeHPRatio:      c.FleeHPRatio,
			HitMemoryTicks:   c.HitMemoryTicks,
			BodyRadius:       c.BodyRadius,
			BodyHeight:       c.BodyHeight,
			BodyHalfLength:   c.BodyHalfLength,
			Leash:            c.Leash,
		}
		if tpl.HP <= 0 {
			return nil, fmt.Errorf("creature %q: hp must be > 0", c.Kind)
		}
		if tpl.MoveInterval <= 0 {
			tpl.MoveInterval = 2
		}
		if tpl.HitMemoryTicks <= 0 {
			tpl.HitMemoryTicks = 5
		}
		// 仇恨传播半径缺省 = 感知半径（保持旧配置行为不变）。
		if tpl.ThreatRadius <= 0 {
			tpl.ThreatRadius = tpl.PerceptionRadius
		}
		// 简化碰撞体由客户端模型推导（cmd/modelcollide → configs/model_collision.json）。
		// 配置里必须显式写出来：加了新生物却忘了跑流水线，在这里 fail fast。
		if tpl.BodyRadius <= 0 || tpl.BodyHeight <= 0 {
			return nil, fmt.Errorf(
				"creature %q: body_radius/body_height 必须 > 0（由客户端模型推导，见 docs/模型到碰撞体流水线.md）",
				c.Kind,
			)
		}
		for _, h := range c.Hostile {
			hk, ok := components.CreatureKindByName[h]
			if !ok {
				return nil, fmt.Errorf("creature %q: unknown hostile kind %q", c.Kind, h)
			}
			tpl.HostileKinds = append(tpl.HostileKinds, hk)
		}
		// 行为树种类（可选）：非法值 fail fast，避免"配错了却静默用默认树"。
		if c.Tree != "" {
			tk, ok := components.TreeKindByName[c.Tree]
			if !ok {
				return nil, fmt.Errorf(
					"creature %q: unknown behavior_tree %q（可选 predator/prey/dormant）",
					c.Kind, c.Tree,
				)
			}
			tpl.TreeKind = tk
		}
		if c.HostilePlayers != nil {
			tpl.HostilePlayers = *c.HostilePlayers
		}
		tpl.Drops = append(tpl.Drops, c.Drops...)
		out[kind] = tpl
	}
	return out, nil
}
