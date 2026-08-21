package worldmap

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/game/components"
)

// biomeTypeByName 配置字符串 → 生物群系枚举（新群系 = 枚举值 + 这里加一行 + biomes.json）。
var biomeTypeByName = map[string]BiomeType{
	"grassland": BiomeGrassland,
	"forest":    BiomeForest,
	"swamp":     BiomeSwamp,
	"snowland":  BiomeSnowland,
	"mine":      BiomeMine,
}

// BiomeSpec 一个生物群系（区域类型）的静态属性：地形规则 / 资源 / 地块效果 / 天气基值。
// 配置驱动：加区域 = biomes.json 加一项 + region_layout 引用它，零代码。
type BiomeSpec struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Terrain     BiomeTerrain      `json:"terrain"`
	Resources   []BiomeResource   `json:"resources"`
	TileEffects []BiomeTileEffect `json:"tile_effects"`
	Weather     WeatherBias       `json:"weather"`
	Drops       []BiomeDrop       `json:"drops,omitempty"`
}

// BiomeDrop 为指定来源追加一条掉落规则。
type BiomeDrop struct {
	Category string              `json:"category"`
	Source   string              `json:"source"`
	Rule     components.DropRule `json:"rule"`
}

// BiomeTerrain 该区域的高度 → 地形映射规则（0 = 用全局默认/继承）。
type BiomeTerrain struct {
	WaterLevel int `json:"water_level"` // h ≤ 此值为水
	RockLevel  int `json:"rock_level"`  // h ≥ 此值为岩
	SnowLevel  int `json:"snow_level"`  // h ≥ 此值为雪；0 = RockLevel+2
}

// BiomeResource 区域资源撒点（每个区域实例按 density 数量散布在区域内）。
type BiomeResource struct {
	Kind    string `json:"kind"`
	Action  string `json:"action"`
	Work    int    `json:"work"`
	Density int    `json:"density"` // 每个区域实例的数量
	MinDist int    `json:"min_dist"`
}

// BiomeTileEffect 区域地块效果：区域内按 coverage 概率铺效果（0..1）。
type BiomeTileEffect struct {
	Effect   string  `json:"effect"`
	Param    int     `json:"param"`
	Coverage float32 `json:"coverage"`
}

// LoadBiomes 读取 biomes.json（区域类型表），按枚举建索引。
// LoadBiomes 读取 biomes.json（区域类型表），按枚举建索引。
func LoadBiomes(path string) (map[BiomeType]BiomeSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Biomes []BiomeSpec `json:"biomes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[BiomeType]BiomeSpec, len(raw.Biomes))
	for _, b := range raw.Biomes {
		if b.Type == "" {
			return nil, fmt.Errorf("biome type required")
		}
		t, ok := biomeTypeByName[b.Type]
		if !ok {
			return nil, fmt.Errorf("unknown biome %q", b.Type)
		}
		for _, drop := range b.Drops {
			switch drop.Category {
			case "resource":
				if _, ok := components.ItemKindByName[drop.Source]; !ok {
					return nil, fmt.Errorf("biome %q: unknown resource drop source %q", b.Type, drop.Source)
				}
			case "creature":
				if _, ok := components.CreatureKindByName[drop.Source]; !ok {
					return nil, fmt.Errorf("biome %q: unknown creature drop source %q", b.Type, drop.Source)
				}
			default:
				return nil, fmt.Errorf("biome %q: invalid drop category %q", b.Type, drop.Category)
			}
		}
		out[t] = b
	}
	return out, nil
}

// RegionLayoutSpec 区域布局（map.json region_layout）：出生区域 biome + 偏好放置规则。
// 联通关系用 near 表达（贴谁放），具体接壤位置由程序随机；BFS 校验兜底保证连通。
type RegionLayoutSpec struct {
	Spawn   string       `json:"spawn"`
	Regions []RegionRule `json:"regions"`
}

// RegionRule 一条区域放置规则（会展开成 Count 个区域实例）。
type RegionRule struct {
	Biome   string   `json:"biome"`
	Count   int      `json:"count"`    // 默认 1
	Near    []string `json:"near"`     // 贴这些区域放："spawn" 或 biome 名
	FarFrom []string `json:"far_from"` // 尽量远离（"spawn" 或 biome 名）
	Size    int      `json:"size"`     // 影响半径，0 = 默认 12（带抖动）
}
