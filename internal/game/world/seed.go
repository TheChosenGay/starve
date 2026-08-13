package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// seedResources 按配置创建可采集实体（按配置顺序，确定性）。
func seedResources(sim *ecs.World, seeds []worldmap.SeededResource) {
	for _, s := range seeds {
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		ecs.Add(sim, e, components.Workable{Kind: s.Kind, Action: s.Action, WorkLeft: s.Work, MaxWork: s.Work})
	}
}

// seedStations 按配置创建工作站实体（Position + Workstation）。
func seedStations(sim *ecs.World, stations []worldmap.StationSeed) {
	for _, s := range stations {
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		ecs.Add(sim, e, components.Workstation{Type: components.WorkstationTypeByName[s.Type]})
	}
}

// seedLoot 生成初始可拾取物资实体（Loot）。
func seedLoot(sim *ecs.World, loots []worldmap.LootSeed) {
	for _, l := range loots {
		k, ok := components.ItemKindByName[l.Kind]
		if !ok || l.Count <= 0 {
			continue
		}
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: l.X, Y: l.Y})
		ecs.Add(sim, e, components.Loot{Items: []components.ItemStack{{Kind: k, Count: l.Count}}})
	}
}

// seedEmitters 生成手摆效果发射器实体（增益植物/火堆等）。
func seedEmitters(sim *ecs.World, emitters []worldmap.EmitterSeed) {
	for _, s := range emitters {
		if len(s.Effects) == 0 || s.Radius < 0 {
			continue
		}
		var instances []components.EffectInstance
		for _, ins := range s.Effects {
			if o, ok := components.EffectOrderByName[ins.Order]; ok {
				instances = append(instances, components.EffectInstance{Order: o, Param: ins.Param})
			}
		}
		if len(instances) == 0 {
			continue
		}
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		ecs.Add(sim, e, components.EffectEmitter{Effects: instances, Radius: s.Radius})
	}
}
