package world

import (
	"encoding/json"
	"fmt"
	"os"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// resourceKindByName 配置字符串 → 资源枚举（新资源 = 枚举值 + 这里加一行 + 模板表）。
var resourceKindByName = map[string]components.ResourceKind{
	"berry": components.ResourceBerry,
	"wood":  components.ResourceWood,
	"flint": components.ResourceFlint,
}

// ResourceSeed 是资源配置表里的一条种子实体（JSON 原始形态）。
type ResourceSeed struct {
	Kind   string `json:"kind"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Count  int    `json:"count"`
	Health int    `json:"health"` // >0 时挂 Health（可被攻击/砍伐，死亡触发掉落）
}

// seededResource 是校验后的种子实体：kind 已解析为枚举。
type seededResource struct {
	kind   components.ResourceKind
	x, y   int
	count  int
	health int
}

// loadResourceSeeds 读取资源配置表（JSON 数组）并校验 kind。
// 未知 kind 直接报错（fail fast），不静默跳过。
func loadResourceSeeds(path string) ([]seededResource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var seeds []ResourceSeed
	if err := json.Unmarshal(data, &seeds); err != nil {
		return nil, err
	}
	out := make([]seededResource, 0, len(seeds))
	for _, s := range seeds {
		k, ok := resourceKindByName[s.Kind]
		if !ok {
			return nil, fmt.Errorf("unknown resource kind %q", s.Kind)
		}
		out = append(out, seededResource{kind: k, x: s.X, y: s.Y, count: s.Count, health: s.Health})
	}
	return out, nil
}

// seedResources 按配置创建可采集实体（按配置顺序，确定性）。
func seedResources(sim *ecs.World, seeds []seededResource) {
	for _, s := range seeds {
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.x, Y: s.y})
		ecs.Add(sim, e, components.Gatherable{Kind: s.kind, Count: s.count})
		if s.health > 0 {
			ecs.Add(sim, e, components.Health{Cur: s.health, Max: s.health})
		}
	}
}
