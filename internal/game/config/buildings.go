package config

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/game/components"
)

// BuildingTemplate 建筑模板（buildings.json）：静态属性，建造时拷贝进 Building 组件。
// 占格尺寸是唯一强制字段；行为组件（如火堆的 HeatSource）由放置逻辑按 Kind 挂载。
type BuildingTemplate struct {
	Name          string
	Width, Height int
}

type buildingJSON struct {
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// loadBuildings 读取 buildings.json（建筑模板表），fail fast。
func loadBuildings(path string) (map[components.BuildingKind]BuildingTemplate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Buildings []buildingJSON `json:"buildings"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	out := make(map[components.BuildingKind]BuildingTemplate, len(raw.Buildings))
	for _, b := range raw.Buildings {
		kind, ok := components.BuildingKindByName[b.Kind]
		if !ok {
			return nil, fmt.Errorf("unknown building kind %q", b.Kind)
		}
		tpl := BuildingTemplate{Name: b.Name, Width: b.Width, Height: b.Height}
		if tpl.Width <= 0 {
			tpl.Width = 1
		}
		if tpl.Height <= 0 {
			tpl.Height = 1
		}
		out[kind] = tpl
	}
	return out, nil
}
