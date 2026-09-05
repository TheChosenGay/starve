package worldmap

import (
	"testing"

	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

func generateConfiguredMap(t *testing.T, seed uint64) (*MapResult, map[BiomeType]BiomeSpec) {
	t.Helper()
	spec, err := LoadMapSpec("../../../configs/map.json")
	if err != nil {
		t.Fatal(err)
	}
	biomes, err := LoadBiomes("../../../configs/biomes.json")
	if err != nil {
		t.Fatal(err)
	}
	return NewMapGenerator(seed, spec, biomes).Generate(), biomes
}

func resourceBiome(res *MapResult, resource SeededResource) BiomeType {
	id := int(res.RegionIDs[resource.Y*res.Width+resource.X])
	if id <= 0 || id > len(res.RegionBiomes) {
		return game.BiomeType_BIOME_TYPE_UNSPECIFIED
	}
	return res.RegionBiomes[id-1]
}

func resourceTerrain(res *MapResult, resource SeededResource) game.TerrainType {
	return game.TerrainType(res.TileTypes[resource.Y*res.Width+resource.X])
}

func TestConfiguredFlowersFormGrasslandClusters(t *testing.T) {
	res, _ := generateConfiguredMap(t, 42)
	var flowers []SeededResource
	for _, resource := range res.Resources {
		if resource.Kind != components.ItemFlower {
			continue
		}
		if resourceTerrain(res, resource) != game.TerrainType_TERRAIN_TYPE_GRASS {
			t.Fatalf("花生成在非草地 (%d,%d): %v", resource.X, resource.Y, resourceTerrain(res, resource))
		}
		if resourceBiome(res, resource) == BiomeGrassland {
			flowers = append(flowers, resource)
		}
	}
	if len(flowers) < 35 {
		t.Fatalf("草原花朵过少: got %d, want >= 35", len(flowers))
	}

	maxNeighbors := 0
	for _, center := range flowers {
		neighbors := 0
		for _, flower := range flowers {
			dx, dy := center.X-flower.X, center.Y-flower.Y
			if dx*dx+dy*dy <= 6*6 {
				neighbors++
			}
		}
		if neighbors > maxNeighbors {
			maxNeighbors = neighbors
		}
	}
	if maxNeighbors < 8 {
		t.Fatalf("未形成明显花簇: 半径 6 内最多 %d 朵，want >= 8", maxNeighbors)
	}
}

func TestConfiguredTreesCoverGrassMountainsAndLeaveForestCorridors(t *testing.T) {
	res, biomes := generateConfiguredMap(t, 42)
	grassTrees, mountainTrees := 0, 0
	forestTrees := make(map[int]int)
	for _, resource := range res.Resources {
		if (resource.Kind == components.ItemWood || resource.Kind == components.ItemFlint) &&
			abs(resource.X-res.SpawnX)+abs(resource.Y-res.SpawnY) <= 3 {
			t.Fatalf("出生点安全圈出现阻挡资源: kind=%v pos=(%d,%d)",
				resource.Kind, resource.X, resource.Y)
		}
		if resource.Kind != components.ItemWood {
			continue
		}
		biome := resourceBiome(res, resource)
		terrain := resourceTerrain(res, resource)
		if biome == BiomeGrassland && terrain == game.TerrainType_TERRAIN_TYPE_GRASS {
			grassTrees++
		}
		if biome == BiomeGrassland && terrain == game.TerrainType_TERRAIN_TYPE_ROCK {
			mountainTrees++
		}
		id := int(res.RegionIDs[resource.Y*res.Width+resource.X])
		if biome == BiomeForest {
			forestTrees[id]++
		}
	}
	if grassTrees == 0 || mountainTrees == 0 {
		t.Fatalf("草地和山地都应有树: grass=%d rock=%d", grassTrees, mountainTrees)
	}

	forestCount := 0
	for i, region := range res.Regions {
		if region.Biome != BiomeForest {
			continue
		}
		forestCount++
		id := i + 1
		if forestTrees[id] < 60 {
			t.Fatalf("森林 %s 树木过少: got %d, want >= 60", region.ID, forestTrees[id])
		}
		halfWidth := biomes[BiomeForest].CorridorWidth
		corridorTiles := 0
		for y := 0; y < res.Height; y++ {
			for x := 0; x < res.Width; x++ {
				if int(res.RegionIDs[y*res.Width+x]) != id ||
					!biomeCorridorContains(region, halfWidth, i, x, y) {
					continue
				}
				corridorTiles++
				if terrain := game.TerrainType(res.TileTypes[y*res.Width+x]); terrain != game.TerrainType_TERRAIN_TYPE_GRASS {
					t.Fatalf("森林 %s 通道不是草地: pos=(%d,%d) terrain=%v", region.ID, x, y, terrain)
				}
			}
		}
		if corridorTiles < 10 {
			t.Fatalf("森林 %s 通道过短: got %d tiles", region.ID, corridorTiles)
		}
		for _, resource := range res.Resources {
			if int(res.RegionIDs[resource.Y*res.Width+resource.X]) != id {
				continue
			}
			if biomeCorridorContains(region, halfWidth, i, resource.X, resource.Y) {
				t.Fatalf("森林 %s 通道被资源占据: kind=%v pos=(%d,%d)",
					region.ID, resource.Kind, resource.X, resource.Y)
			}
		}
	}
	if forestCount != 2 {
		t.Fatalf("森林数量=%d, want 2", forestCount)
	}
}
