package config

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// loadResourceSeeds 读取资源配置表（JSON 数组）并校验 kind。
// 未知 kind 直接报错（fail fast），不静默跳过。
func loadResourceSeeds(path string) ([]worldmap.SeededResource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var seeds []worldmap.ResourceSeed
	if err := json.Unmarshal(data, &seeds); err != nil {
		return nil, err
	}
	out := make([]worldmap.SeededResource, 0, len(seeds))
	for _, s := range seeds {
		k, ok := components.ItemKindByName[s.Kind]
		if !ok {
			return nil, fmt.Errorf("unknown resource kind %q", s.Kind)
		}
		action, ok := components.WorkActionByName[s.Action]
		if !ok {
			return nil, fmt.Errorf("unknown work action %q for kind %q", s.Action, s.Kind)
		}
		if s.Work <= 0 {
			return nil, fmt.Errorf("work must be > 0 for kind %q", s.Kind)
		}
		out = append(out, worldmap.SeededResource{Kind: k, X: s.X, Y: s.Y, Action: action, Work: s.Work})
	}
	return out, nil
}
