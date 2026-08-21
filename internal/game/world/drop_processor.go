package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/config"
	"starve/internal/game/worldmap"
)

const dropPlacementRadius = 2

// DropProcessor 编排来源识别后的掉落流程：补齐 biome 上下文、解析规则、
// 安排位置并创建独立 Lootable 实体。它不负责来源进入 Dead 的判定。
type DropProcessor struct {
	sim      *ecs.World
	resolver config.TableDropResolver
	mapSeed  uint64
}

func NewDropProcessor(sim *ecs.World, resolver config.TableDropResolver, mapSeed uint64) *DropProcessor {
	return &DropProcessor{sim: sim, resolver: resolver, mapSeed: mapSeed}
}

// Process 只消费 Dead+DropSource；每种物品合并成一个 stack，
// 并为每个 stack 创建一个独立 Lootable 实体。
func (p *DropProcessor) Process(tick int64) {
	var sources []ecs.Entity
	ecs.Query[components.DropSource](p.sim, func(e ecs.Entity, _ *components.DropSource) {
		if ecs.Has[components.Dead](p.sim, e) {
			sources = append(sources, e)
		}
	})
	for _, entity := range sources {
		p.processSource(entity, tick)
	}
}

func (p *DropProcessor) processSource(entity ecs.Entity, tick int64) {
	source := *ecs.Get[components.DropSource](p.sim, entity)
	origin := components.Position{}
	if ecs.Has[components.Position](p.sim, entity) {
		origin = *ecs.Get[components.Position](p.sim, entity)
	}

	mapData, hasMap := ecs.TryResource[MapData](p.sim)
	biome := worldmap.BiomeType(0)
	if hasMap {
		biome = mapData.BiomeAt(origin.X, origin.Y)
	}
	context := config.DropContext{
		MapSeed:      p.mapSeed,
		Tick:         tick,
		SourceEntity: uint64(entity),
		Category:     source.Category,
		ResourceKind: source.ResourceKind,
		CreatureKind: source.CreatureKind,
		Biome:        biome,
	}
	stacks := p.resolver.Resolve(context)

	// 资源占格必须先解除，附近位置采样才能把来源格视为可走。
	if source.Category == components.DropSourceResource {
		removeWorkTarget(p.sim, entity)
		if ecs.Has[components.Block](p.sim, entity) {
			ecs.Remove[components.Block](p.sim, entity)
		}
	}

	positions := make([]components.Position, len(stacks))
	if hasMap {
		positions = mapData.NearbyWalkable(
			origin,
			len(stacks),
			dropPlacementRadius,
			dropPlacementSeed(context),
		)
	} else {
		for i := range positions {
			positions[i] = origin
		}
	}
	for i, stack := range stacks {
		loot := p.sim.CreateEntity()
		ecs.Add(p.sim, loot, positions[i])
		ecs.Add(p.sim, loot, components.Lootable{Items: []components.ItemStack{stack}})
	}

	ecs.Remove[components.DropSource](p.sim, entity)
	switch source.Category {
	case components.DropSourceResource:
		// Loot 创建完成后再销毁，避免 ECS free-list 在本 tick 复用来源 ID。
		p.sim.DestroyEntity(entity)
	case components.DropSourceCreature:
		// 保留 Dead 尸体，继续由 cleanupCorpses 回收；行为系统已按 Dead 停止活动。
		ecs.Remove[components.Creature](p.sim, entity)
	}
}

func removeWorkTarget(sim *ecs.World, entity ecs.Entity) {
	ecs.Remove[interactive.Choppable](sim, entity)
	ecs.Remove[interactive.Minable](sim, entity)
	ecs.Remove[interactive.Pickable](sim, entity)
}

func dropPlacementSeed(ctx config.DropContext) uint64 {
	return ctx.MapSeed ^ uint64(ctx.Tick)*0x517cc1b727220a95 ^ ctx.SourceEntity*0x6eed0e9da4d94a4f
}
