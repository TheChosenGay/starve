package config

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// Recipe 制作配方：输出 + 材料 + 工作站要求 + 制作时长（tick）。
// 配置驱动：加配方 = 在 crafting.json 加一行，零代码。
type Recipe struct {
	ID          string
	Workstation components.WorkstationType // 0 = 徒手可做
	Ticks       int
	Output      components.ItemStack
	Ingredients []components.ItemStack
}

type itemRef struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
}

type recipeJSON struct {
	ID          string    `json:"id"`
	Workstation string    `json:"workstation"`
	Ticks       int       `json:"ticks"`
	Output      itemRef   `json:"output"`
	Ingredients []itemRef `json:"ingredients"`
}

// loadRecipes 读取配方表（crafting.json），校验并补默认值。
func loadRecipes(path string) (map[string]Recipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Recipes []recipeJSON `json:"recipes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[string]Recipe, len(raw.Recipes))
	for _, r := range raw.Recipes {
		if r.ID == "" {
			return nil, fmt.Errorf("recipe id required")
		}
		okind, ok := components.ItemKindByName[r.Output.Kind]
		if !ok {
			return nil, fmt.Errorf("recipe %q: unknown output kind %q", r.ID, r.Output.Kind)
		}
		recipe := Recipe{
			ID:     r.ID,
			Ticks:  r.Ticks,
			Output: components.ItemStack{Kind: okind, Count: r.Output.Count},
		}
		if r.Workstation != "" {
			wt, ok := components.WorkstationTypeByName[r.Workstation]
			if !ok {
				return nil, fmt.Errorf("recipe %q: unknown workstation %q", r.ID, r.Workstation)
			}
			recipe.Workstation = wt
		}
		if recipe.Ticks <= 0 {
			recipe.Ticks = 10
		}
		if recipe.Output.Count <= 0 {
			recipe.Output.Count = 1
		}
		for _, ing := range r.Ingredients {
			ik, ok := components.ItemKindByName[ing.Kind]
			if !ok {
				return nil, fmt.Errorf("recipe %q: unknown ingredient kind %q", r.ID, ing.Kind)
			}
			if ing.Count <= 0 {
				return nil, fmt.Errorf("recipe %q: ingredient count must be > 0", r.ID)
			}
			recipe.Ingredients = append(recipe.Ingredients, components.ItemStack{Kind: ik, Count: ing.Count})
		}
		out[r.ID] = recipe
	}
	return out, nil
}

// loadStations 读取工作站配置（stations.json）。
func loadStations(path string) ([]worldmap.StationSeed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []worldmap.StationSeed
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make([]worldmap.StationSeed, 0, len(raw))
	for _, s := range raw {
		if _, ok := components.WorkstationTypeByName[s.Type]; !ok {
			return nil, fmt.Errorf("unknown station type %q", s.Type)
		}
		out = append(out, s)
	}
	return out, nil
}
