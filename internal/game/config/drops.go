package config

import (
	"math/rand"
	"sort"

	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// DropContext 是掉落解析所需的完整运行时输入，不持有 ECS 或地图对象。
type DropContext struct {
	MapSeed      uint64
	Tick         int64
	SourceEntity uint64
	Category     components.DropSourceCategory
	ResourceKind components.ItemKind
	CreatureKind components.CreatureKind
	Biome        worldmap.BiomeType
}

// TableDropResolver 按静态配置表和局部确定性随机源解析默认及 biome 追加掉落。
type TableDropResolver struct {
	Templates map[components.ItemKind]ItemTemplate
	Creatures map[components.CreatureKind]CreatureTemplate
	Biomes    map[worldmap.BiomeType]worldmap.BiomeSpec
}

func (r TableDropResolver) Resolve(ctx DropContext) []components.ItemStack {
	rules := r.defaultRules(ctx)
	if biome, ok := r.Biomes[ctx.Biome]; ok {
		for _, extra := range biome.Drops {
			if matchesBiomeDrop(extra, ctx) {
				rules = append(rules, extra.Rule)
			}
		}
	}
	rng := rand.New(rand.NewSource(int64(dropSeed(ctx))))
	counts := make(map[components.ItemKind]int)
	for _, rule := range rules {
		if rule.Kind == 0 || rule.MinCount <= 0 || rule.MaxCount < rule.MinCount || rule.Chance <= 0 {
			continue
		}
		if rule.Chance < components.DropChanceScale && rng.Intn(components.DropChanceScale) >= rule.Chance {
			continue
		}
		count := rule.MinCount
		if rule.MaxCount > rule.MinCount {
			count += rng.Intn(rule.MaxCount - rule.MinCount + 1)
		}
		counts[rule.Kind] += count
	}
	kinds := make([]int, 0, len(counts))
	for kind, count := range counts {
		if count > 0 {
			kinds = append(kinds, int(kind))
		}
	}
	sort.Ints(kinds)
	out := make([]components.ItemStack, 0, len(kinds))
	for _, value := range kinds {
		kind := components.ItemKind(value)
		out = append(out, components.ItemStack{Kind: kind, Count: counts[kind]})
	}
	return out
}

func (r TableDropResolver) defaultRules(ctx DropContext) []components.DropRule {
	switch ctx.Category {
	case components.DropSourceResource:
		return append([]components.DropRule(nil), r.Templates[ctx.ResourceKind].DropTable...)
	case components.DropSourceCreature:
		return append([]components.DropRule(nil), r.Creatures[ctx.CreatureKind].Drops...)
	default:
		return nil
	}
}

func matchesBiomeDrop(drop worldmap.BiomeDrop, ctx DropContext) bool {
	switch ctx.Category {
	case components.DropSourceResource:
		kind, ok := components.ItemKindByName[drop.Source]
		return drop.Category == "resource" && ok && kind == ctx.ResourceKind
	case components.DropSourceCreature:
		kind, ok := components.CreatureKindByName[drop.Source]
		return drop.Category == "creature" && ok && kind == ctx.CreatureKind
	default:
		return false
	}
}

func dropSeed(ctx DropContext) uint64 {
	x := ctx.MapSeed ^ uint64(ctx.Tick)*0x9e3779b97f4a7c15
	x ^= ctx.SourceEntity * 0xbf58476d1ce4e5b9
	x ^= uint64(ctx.Category)<<56 | uint64(ctx.ResourceKind)<<24 | uint64(ctx.CreatureKind)<<8 | uint64(ctx.Biome)
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}
