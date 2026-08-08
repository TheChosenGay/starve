package world

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// Recipe 制作配方：输出 + 材料 + 工作站要求 + 制作时长（tick）。
// 配置驱动：加配方 = 在 crafting.json 加一行，零代码。
type Recipe struct {
	ID          string
	Workstation string
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
		okind, ok := itemKindByName[r.Output.Kind]
		if !ok {
			return nil, fmt.Errorf("recipe %q: unknown output kind %q", r.ID, r.Output.Kind)
		}
		recipe := Recipe{
			ID:          r.ID,
			Workstation: r.Workstation,
			Ticks:       r.Ticks,
			Output:      components.ItemStack{Kind: okind, Count: r.Output.Count},
		}
		if recipe.Ticks <= 0 {
			recipe.Ticks = 10
		}
		if recipe.Output.Count <= 0 {
			recipe.Output.Count = 1
		}
		for _, ing := range r.Ingredients {
			ik, ok := itemKindByName[ing.Kind]
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

// StationSeed 工作站配置：类型 + 坐标。
type StationSeed struct {
	Type string `json:"type"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

// loadStations 读取工作站配置（stations.json）。
func loadStations(path string) ([]StationSeed, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []StationSeed
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// seedStations 按配置创建工作站实体（Position + Workstation）。
func seedStations(sim *ecs.World, stations []StationSeed) {
	for _, s := range stations {
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		ecs.Add(sim, e, components.Workstation{Type: s.Type})
	}
}
