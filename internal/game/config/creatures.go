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
	AttackRange      int
	AttackDamage     int
	AttackCooldown   int                       // 攻击间隔（tick）
	RoamRadius       int                       // 游荡半径（围绕出生点）
	FleeHPRatio      float32                   // 血量低于该比例切 flee（0 = 永不逃跑）
	HitMemoryTicks   int                       // 受击记忆窗口（tick）
	HostileKinds     []components.CreatureKind // 视为敌对的生物类型（玩家隐式敌对）
	HostilePlayers   bool                      // 玩家是否视为敌对
	Drops            []components.ItemStack
}

type creatureJSON struct {
	Kind             string   `json:"kind"`
	Name             string   `json:"name"`
	HP               int      `json:"hp"`
	MoveInterval     int      `json:"move_interval"`
	PerceptionRadius int      `json:"perception_radius"`
	AttackRange      int      `json:"attack_range"`
	AttackDamage     int      `json:"attack_damage"`
	AttackCooldown   int      `json:"attack_cooldown"`
	RoamRadius       int      `json:"roam_radius"`
	FleeHPRatio      float32  `json:"flee_hp_ratio"`
	HitMemoryTicks   int      `json:"hit_memory_ticks"`
	Hostile          []string `json:"hostile"`
	HostilePlayers   *bool    `json:"hostile_players"` // 指针：缺省 false（友好）
	Drops            []struct {
		Kind  string `json:"kind"`
		Count int    `json:"count"`
	} `json:"drops"`
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
			AttackRange:      c.AttackRange,
			AttackDamage:     c.AttackDamage,
			AttackCooldown:   c.AttackCooldown,
			RoamRadius:       c.RoamRadius,
			FleeHPRatio:      c.FleeHPRatio,
			HitMemoryTicks:   c.HitMemoryTicks,
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
		for _, h := range c.Hostile {
			hk, ok := components.CreatureKindByName[h]
			if !ok {
				return nil, fmt.Errorf("creature %q: unknown hostile kind %q", c.Kind, h)
			}
			tpl.HostileKinds = append(tpl.HostileKinds, hk)
		}
		if c.HostilePlayers != nil {
			tpl.HostilePlayers = *c.HostilePlayers
		}
		for _, d := range c.Drops {
			dk, ok := components.ItemKindByName[d.Kind]
			if !ok || d.Count <= 0 {
				return nil, fmt.Errorf("creature %q: bad drop %q", c.Kind, d.Kind)
			}
			tpl.Drops = append(tpl.Drops, components.ItemStack{Kind: dk, Count: d.Count})
		}
		out[kind] = tpl
	}
	return out, nil
}
