package world

import (
	"fmt"
	"sort"

	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// GameConfig 世界静态配置（资源/模板/配方/工作站）的集中加载与端上序列化。
// 职责：读配置表 + 校验 + 转成端上契约（登录时推 world.config，客户端据此渲染）。
type GameConfig struct {
	Resources []seededResource
	Templates map[components.ItemKind]ItemTemplate
	Recipes   map[string]Recipe
	Stations  []StationSeed
}

// LoadGameConfig 加载全部配置表（失败即返回错误，由调用方决定兜底/退出）。
func LoadGameConfig(cfg WorldConfig) (*GameConfig, error) {
	gc := &GameConfig{
		Templates: map[components.ItemKind]ItemTemplate{},
		Recipes:   map[string]Recipe{},
	}
	if cfg.ResourcesPath != "" {
		seeds, err := loadResourceSeeds(cfg.ResourcesPath)
		if err != nil {
			return nil, fmt.Errorf("resources: %w", err)
		}
		gc.Resources = seeds
	}
	if cfg.TemplatesPath != "" {
		ts, err := loadTemplates(cfg.TemplatesPath)
		if err != nil {
			return nil, fmt.Errorf("templates: %w", err)
		}
		gc.Templates = ts
	}
	if cfg.RecipesPath != "" {
		rs, err := loadRecipes(cfg.RecipesPath)
		if err != nil {
			return nil, fmt.Errorf("recipes: %w", err)
		}
		gc.Recipes = rs
	}
	if cfg.StationsPath != "" {
		ss, err := loadStations(cfg.StationsPath)
		if err != nil {
			return nil, fmt.Errorf("stations: %w", err)
		}
		gc.Stations = ss
	}
	return gc, nil
}

// ToProto 把配置编码成端上契约（模板/配方/工作站，确定性排序）。
func (g *GameConfig) ToProto() *game.GameConfig {
	out := &game.GameConfig{}
	kinds := make([]int, 0, len(g.Templates))
	for k := range g.Templates {
		kinds = append(kinds, int(k))
	}
	sort.Ints(kinds)
	for _, k := range kinds {
		kind := components.ItemKind(k)
		t := g.Templates[kind]
		tc := &game.TemplateConfig{
			Kind:         kind,
			Name:         t.Name,
			Color:        t.Color,
			StackSize:    int32(t.StackSize),
			RespawnTicks: int32(t.RespawnTicks),
		}
		if t.Tool != nil {
			tc.Tool = &game.ToolConfig{Action: t.Tool.Action, Efficiency: int32(t.Tool.Efficiency), Durability: int32(t.Tool.Durability)}
		}
		if t.UseEffect != nil {
			tc.UseEffect = &game.UseEffectConfig{Hunger: int32(t.UseEffect.Hunger), Health: int32(t.UseEffect.Health)}
		}
		for _, d := range t.DropTable {
			dk, ok := itemKindByName[d.Kind]
			if !ok || d.Count <= 0 {
				continue
			}
			tc.DropTable = append(tc.DropTable, &game.DropConfig{Kind: dk, Count: int32(d.Count)})
		}
		out.Templates = append(out.Templates, tc)
	}

	ids := make([]string, 0, len(g.Recipes))
	for id := range g.Recipes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := g.Recipes[id]
		rc := &game.RecipeConfig{
			Id:          id,
			Workstation: r.Workstation,
			Ticks:       int32(r.Ticks),
			Output:      &game.ItemRefConfig{Kind: r.Output.Kind, Count: int32(r.Output.Count)},
		}
		for _, ing := range r.Ingredients {
			rc.Ingredients = append(rc.Ingredients, &game.ItemRefConfig{Kind: ing.Kind, Count: int32(ing.Count)})
		}
		out.Recipes = append(out.Recipes, rc)
	}

	for _, s := range g.Stations {
		wt, ok := workstationTypeByName[s.Type]
		if !ok {
			continue
		}
		out.Stations = append(out.Stations, &game.StationConfig{Type: wt, X: int32(s.X), Y: int32(s.Y)})
	}
	return out
}
