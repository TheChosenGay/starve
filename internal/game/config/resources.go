package config

import (
	"encoding/json"
	"os"

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
		kind, action, err := worldmap.ResolveResourceSpec(s.Kind, s.Action, s.Work)
		if err != nil {
			return nil, err
		}
		out = append(out, worldmap.SeededResource{Kind: kind, X: s.X, Y: s.Y, Action: action, Work: s.Work})
	}
	return out, nil
}
