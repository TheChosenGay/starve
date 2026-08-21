package world

import (
	"reflect"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/config"
	"starve/internal/game/worldmap"
)

func findLootableKind(t *testing.T, wa *WorldActor, kind components.ItemKind) ecs.Entity {
	t.Helper()
	var found ecs.Entity
	ecs.Query[components.Lootable](wa.sim, func(e ecs.Entity, loot *components.Lootable) {
		if found == 0 && len(loot.Items) == 1 && loot.Items[0].Kind == kind {
			found = e
		}
	})
	if found == 0 {
		t.Fatalf("未找到 %v 的独立掉落实体", kind)
	}
	return found
}

func TestTableDropResolverDefaultBiomeMergeAndDeterminism(t *testing.T) {
	resolver := config.TableDropResolver{
		Templates: map[components.ItemKind]config.ItemTemplate{
			components.ItemWood: {DropTable: []components.DropRule{
				{Kind: components.ItemWood, MinCount: 2, MaxCount: 2, Chance: components.DropChanceScale},
				{Kind: components.ItemReed, MinCount: 1, MaxCount: 3, Chance: components.DropChanceScale},
				{Kind: components.ItemRareOre, MinCount: 1, MaxCount: 1, Chance: 0},
			}},
		},
		Biomes: map[worldmap.BiomeType]worldmap.BiomeSpec{
			worldmap.BiomeSwamp: {Drops: []worldmap.BiomeDrop{{
				Category: "resource", Source: "wood",
				Rule: components.DropRule{Kind: components.ItemWood, MinCount: 3, MaxCount: 3, Chance: components.DropChanceScale},
			}}},
		},
	}
	ctx := config.DropContext{
		MapSeed: 42, Tick: 7, SourceEntity: 99, Category: components.DropSourceResource,
		ResourceKind: components.ItemWood, Biome: worldmap.BiomeSwamp,
	}
	got := resolver.Resolve(ctx)
	if !reflect.DeepEqual(got, resolver.Resolve(ctx)) {
		t.Fatal("相同上下文应产生完全相同的掉落")
	}
	if len(got) != 2 || got[0].Kind != components.ItemWood || got[0].Count != 5 ||
		got[1].Kind != components.ItemReed || got[1].Count < 1 || got[1].Count > 3 {
		t.Fatalf("默认+biome 合并结果错误: %+v", got)
	}
}

func TestDropPipelineCreatesIndependentEntitiesOnce(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wa.config.Templates[components.ItemWood] = config.ItemTemplate{DropTable: []components.DropRule{
		{Kind: components.ItemWood, MinCount: 2, MaxCount: 2, Chance: components.DropChanceScale},
		{Kind: components.ItemWood, MinCount: 1, MaxCount: 1, Chance: components.DropChanceScale},
		{Kind: components.ItemReed, MinCount: 1, MaxCount: 1, Chance: components.DropChanceScale},
	}}
	source := wa.sim.CreateEntity()
	ecs.Add(wa.sim, source, components.Position{X: 4, Y: 5})
	ecs.Add(wa.sim, source, components.Dead{Reason: "test"})
	ecs.Add(wa.sim, source, components.DropSource{Category: components.DropSourceResource, ResourceKind: components.ItemWood})

	wa.processDrops()
	if wa.sim.IsAlive(source) {
		t.Fatal("资源来源掉落后应销毁")
	}
	count := 0
	seen := map[components.ItemKind]int{}
	ecs.Query[components.Lootable](wa.sim, func(e ecs.Entity, loot *components.Lootable) {
		count++
		if e == source || len(loot.Items) != 1 || ecs.Has[components.Dead](wa.sim, e) || ecs.Has[components.Block](wa.sim, e) {
			t.Fatalf("掉落实体结构错误: entity=%d loot=%+v", e, loot)
		}
		seen[loot.Items[0].Kind] = loot.Items[0].Count
	})
	if count != 2 || seen[components.ItemWood] != 3 || seen[components.ItemReed] != 1 {
		t.Fatalf("每 stack 一个实体且同 kind 合并失败: count=%d seen=%v", count, seen)
	}
	wa.processDrops()
	again := 0
	ecs.Query[components.Lootable](wa.sim, func(ecs.Entity, *components.Lootable) { again++ })
	if again != count {
		t.Fatalf("重复处理产生额外掉落: %d -> %d", count, again)
	}
}

func TestDropPipelineKeepsCreatureCorpse(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wa.config.Creatures[components.CreatureWolf] = config.CreatureTemplate{Drops: []components.DropRule{{
		Kind: components.ItemMeat, MinCount: 2, MaxCount: 2, Chance: components.DropChanceScale,
	}}}
	source := wa.sim.CreateEntity()
	ecs.Add(wa.sim, source, components.Position{X: 1, Y: 2})
	ecs.Add(wa.sim, source, components.Dead{Reason: "test"})
	ecs.Add(wa.sim, source, components.Creature{Kind: components.CreatureWolf})
	ecs.Add(wa.sim, source, components.DropSource{Category: components.DropSourceCreature, CreatureKind: components.CreatureWolf})

	wa.processDrops()
	if !wa.sim.IsAlive(source) || !ecs.Has[components.Dead](wa.sim, source) ||
		ecs.Has[components.Creature](wa.sim, source) || ecs.Has[components.DropSource](wa.sim, source) ||
		ecs.Has[components.Lootable](wa.sim, source) {
		t.Fatal("生物来源应保留纯 Dead 尸体并消费 Creature/DropSource")
	}
	if loot := findLootableKind(t, wa, components.ItemMeat); loot == source {
		t.Fatal("生物掉落必须与尸体分离")
	}
}

func TestLoadMigratesDropSourcesWithoutDuplicatingLegacyLoot(t *testing.T) {
	old := NewWorldActor(WorldConfig{})
	resource := old.sim.CreateEntity()
	ecs.Add(old.sim, resource, components.Position{X: 1, Y: 1})
	ecs.Add(old.sim, resource, interactive.Choppable{Kind: components.ItemWood, WorkLeft: 2, MaxWork: 2})

	legacyLoot := old.sim.CreateEntity()
	ecs.Add(old.sim, legacyLoot, components.Position{X: 2, Y: 2})
	ecs.Add(old.sim, legacyLoot, interactive.Choppable{Kind: components.ItemWood, WorkLeft: 0, MaxWork: 2})
	ecs.Add(old.sim, legacyLoot, components.Dead{Reason: "worked"})
	ecs.Add(old.sim, legacyLoot, components.Lootable{Items: []components.ItemStack{{Kind: components.ItemWood, Count: 3}}})

	creature := old.sim.CreateEntity()
	ecs.Add(old.sim, creature, components.Position{X: 3, Y: 3})
	ecs.Add(old.sim, creature, components.Creature{
		Kind:  components.CreatureWolf,
		Drops: []components.ItemStack{{Kind: components.ItemMeat, Count: 2}},
	})

	loaded := NewWorldActor(WorldConfig{})
	if err := loaded.Load(old.Save()); err != nil {
		t.Fatal(err)
	}
	if !ecs.Has[components.DropSource](loaded.sim, resource) {
		t.Fatal("旧资源应补挂 DropSource")
	}
	if ecs.Has[components.DropSource](loaded.sim, legacyLoot) {
		t.Fatal("已经就地 Lootable 的旧状态不应补挂 DropSource")
	}
	if !ecs.Has[components.DropSource](loaded.sim, creature) {
		t.Fatal("旧 Creature 应补挂 DropSource")
	}
	loaded.processDrops()
	lootCount := 0
	ecs.Query[components.Lootable](loaded.sim, func(ecs.Entity, *components.Lootable) { lootCount++ })
	if lootCount != 1 {
		t.Fatalf("旧 Lootable 不应重复掉落: count=%d", lootCount)
	}
}
