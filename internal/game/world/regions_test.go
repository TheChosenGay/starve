package world

import (
	"os"
	"path/filepath"
	"testing"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/weather"
	"starve/internal/game/worldmap"
	game "starve/pkg/proto/game"
)

const regionTestBiomesJSON = `{
  "biomes": [
    { "type": "grassland", "name": "草原", "terrain": { "water_level": 1, "rock_level": 5 },
      "resources": [ { "kind": "berry", "action": "pick", "work": 3, "density": 4, "min_dist": 2 } ],
      "tile_effects": [], "weather": { "temp_bias": 0, "fog_bias": 0, "rain_bias": 0 } },
    { "type": "forest", "name": "森林", "terrain": { "water_level": 1, "rock_level": 5 },
      "resources": [ { "kind": "wood", "action": "chop", "work": 5, "density": 6, "min_dist": 3 } ],
      "tile_effects": [], "weather": { "fog_bias": 0.1 } },
    { "type": "swamp", "name": "沼泽", "terrain": { "water_level": 3, "rock_level": 5 },
      "resources": [ { "kind": "berry", "action": "pick", "work": 2, "density": 3, "min_dist": 2 } ],
      "tile_effects": [ { "effect": "poison", "param": 2, "coverage": 0.5 } ],
      "weather": { "temp_bias": -3, "fog_bias": 0.25, "rain_bias": 0.1 } },
    { "type": "snowland", "name": "雪原", "terrain": { "water_level": 1, "rock_level": 5, "snow_level": 3 },
      "resources": [ { "kind": "flint", "action": "mine", "work": 3, "density": 4, "min_dist": 3 } ],
      "tile_effects": [ { "effect": "speed", "param": -50, "coverage": 0.4 } ],
      "weather": { "temp_bias": -12, "fog_bias": 0.1, "rain_bias": -0.05 } },
    { "type": "mine", "name": "矿区", "terrain": { "water_level": 1, "rock_level": 4 },
      "resources": [ { "kind": "flint", "action": "mine", "work": 3, "density": 5, "min_dist": 2 } ],
      "tile_effects": [], "weather": { "temp_bias": 0, "fog_bias": 0, "rain_bias": 0 } }
  ]
}`

const regionTestMapJSON = `{
  "width": 60,
  "height": 60,
  "spawn_x": 30,
  "spawn_y": 30,
  "terrain": { "hills": 8, "max_amp": 4, "rock_level": 5, "water_level": 0, "spawn_flat_radius": 6, "base_level": 2 },
  "region_layout": {
    "spawn": "grassland",
    "regions": [
      { "biome": "forest", "count": 2, "near": ["spawn"], "size": 12 },
      { "biome": "swamp", "count": 1, "near": ["forest"] },
      { "biome": "snowland", "count": 1, "near": ["forest"] },
      { "biome": "mine", "count": 1, "near": ["forest"] }
    ]
  },
  "handplaced": {},
  "scatter": []
}`

func regionTestPaths(t *testing.T) (mapPath, biomesPath string) {
	t.Helper()
	dir := t.TempDir()
	mapPath = filepath.Join(dir, "map.json")
	biomesPath = filepath.Join(dir, "biomes.json")
	if err := os.WriteFile(mapPath, []byte(regionTestMapJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(biomesPath, []byte(regionTestBiomesJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return mapPath, biomesPath
}

func genRegionMap(t *testing.T, seed uint64) *MapResult {
	t.Helper()
	mapPath, biomesPath := regionTestPaths(t)
	spec, err := worldmap.LoadMapSpec(mapPath)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := worldmap.LoadBiomes(biomesPath)
	if err != nil {
		t.Fatal(err)
	}
	return worldmap.NewMapGenerator(seed, spec, bs).Generate()
}

// 确定性：同 seed 区域布局一致；不同 seed 不同。
func TestRegionLayoutDeterministic(t *testing.T) {
	a := genRegionMap(t, 42)
	b := genRegionMap(t, 42)
	if len(a.RegionIDs) != 60*60 || len(a.Regions) != 6 {
		t.Fatalf("区域实例=%d, ids=%d, want 6 / 3600", len(a.Regions), len(a.RegionIDs))
	}
	for i := range a.RegionIDs {
		if a.RegionIDs[i] != b.RegionIDs[i] {
			t.Fatal("同 seed 区域布局不一致")
		}
	}
	c := genRegionMap(t, 43)
	diff := false
	for i := range a.RegionIDs {
		if a.RegionIDs[i] != c.RegionIDs[i] {
			diff = true
			break
		}
	}
	if !diff {
		t.Fatal("不同 seed 区域布局不应一致")
	}
}

// 连通性：出生点 BFS 可走格子应覆盖全部区域（一块大陆）。
func TestRegionLayoutConnected(t *testing.T) {
	res := genRegionMap(t, 42)
	if !worldmap.ValidateRegionConnectivity(res.RegionIDs, res.TileTypes, res.Width, res.Height, res.SpawnX, res.SpawnY) {
		for i, r := range res.Regions {
			tiles, water := 0, 0
			for idx, id := range res.RegionIDs {
				if int(id) == i+1 {
					tiles++
					if res.TileTypes[idx] == byte(game.TerrainType_TERRAIN_TYPE_WATER) {
						water++
					}
				}
			}
			t.Logf("region %s(%s) center=(%d,%d) tiles=%d water=%d", r.ID, r.Biome, r.X, r.Y, tiles, water)
		}
		t.Fatal("区域未全部连通（存在孤立区域）")
	}
	// 出生格应为安全草地
	if got := game.TerrainType(res.TileTypes[res.SpawnY*res.Width+res.SpawnX]); got != game.TerrainType_TERRAIN_TYPE_GRASS {
		t.Fatalf("出生格地形=%v want grass", got)
	}
}

// 区域内容：每个 biome 的资源撒在自己区域内；沼泽有中毒地块效果。
func TestRegionBiomeContent(t *testing.T) {
	res := genRegionMap(t, 42)
	byBiome := map[worldmap.BiomeType]byte{}
	for i, r := range res.Regions {
		byBiome[r.Biome] = byte(i + 1)
	}
	// 资源归属
	woodInForest, flintInMine := false, false
	for _, r := range res.Resources {
		regionID := res.RegionIDs[r.Y*res.Width+r.X]
		if regionID == byBiome[worldmap.BiomeForest] && r.Kind == components.ItemWood {
			woodInForest = true
		}
		if regionID == byBiome[worldmap.BiomeMine] && r.Kind == components.ItemFlint {
			flintInMine = true
		}
	}
	if !woodInForest {
		t.Fatal("森林区域应撒木材资源")
	}
	if !flintInMine {
		t.Fatal("矿区应撒燧石资源")
	}
	// 沼泽毒效果
	swampPoison := false
	for i := 0; i < len(res.TileEffects); i++ {
		if res.RegionIDs[i] == byBiome[worldmap.BiomeSwamp] && res.TileEffects[i] == byte(components.EffectPoison) {
			swampPoison = true
			break
		}
	}
	if !swampPoison {
		t.Fatal("沼泽区域应有中毒地块效果")
	}
}

// 单元级：区域 terrain 规则决定高度→地形映射。
func TestRegionTileTypeRules(t *testing.T) {
	g := worldmap.NewMapGenerator(1,
		&MapSpec{Width: 10, Height: 10, SpawnX: 100, SpawnY: 100, Terrain: HeightSpec{WaterLevel: 1, RockLevel: 6, SpawnFlatRadius: 0}},
		map[worldmap.BiomeType]BiomeSpec{
			worldmap.BiomeSwamp:    {Terrain: BiomeTerrain{WaterLevel: 3, RockLevel: 6}},
			worldmap.BiomeSnowland: {Terrain: BiomeTerrain{WaterLevel: 1, RockLevel: 5, SnowLevel: 3}},
		},
	)
	res := &MapResult{Width: 10, Height: 10, SpawnX: 100, SpawnY: 100}
	regions := []RegionInstance{
		{ID: "swamp", Biome: worldmap.BiomeSwamp},
		{ID: "snow", Biome: worldmap.BiomeSnowland},
	}
	ids := make([]byte, 100)
	ids[55] = 1 // 区域 1
	ids[66] = 2 // 区域 2
	if got := g.RegionTileType(2, 5, 5, res, regions, ids); got != game.TerrainType_TERRAIN_TYPE_WATER {
		t.Fatalf("沼泽 h=2 应为水, got %v", got)
	}
	if got := g.RegionTileType(3, 6, 6, res, regions, ids); got != game.TerrainType_TERRAIN_TYPE_SNOW {
		t.Fatalf("雪原 h=3 应为雪, got %v", got)
	}
}

// 天气基值：沼泽采样比草原更冷/更雾。
func TestRegionWeatherBias(t *testing.T) {
	mapPath, biomesPath := regionTestPaths(t)
	wa := NewWorldActor(WorldConfig{MapPath: mapPath, BiomesPath: biomesPath, MapSeed: 42})
	md := ecs.Resource[worldmap.MapData](wa.sim)
	// 找一块"寒冷/多雾"偏置区域（沼泽/雪原）和一块无偏置区域（草原）
	coldX, coldY, flatX, flatY := -1, -1, -1, -1
	for y := 0; y < md.Height && (coldX < 0 || flatX < 0); y++ {
		for x := 0; x < md.Width && (coldX < 0 || flatX < 0); x++ {
			id := md.TileRegionAt(x, y)
			if id == 0 {
				continue
			}
			if coldX < 0 && md.RegionBiasAt(x, y).Temp < -2 {
				coldX, coldY = x, y
			}
			if flatX < 0 && md.RegionBiasAt(x, y) == (WeatherBias{}) {
				flatX, flatY = x, y
			}
		}
	}
	if coldX < 0 || flatX < 0 {
		t.Fatal("未找到沼泽/草原格")
	}
	q := weather.WeatherQuery{Tick: 100, Season: components.SeasonSummer}
	q.X, q.Y = coldX, coldY
	cold := weather.SampleAt(wa.sim, q)
	q.X, q.Y = flatX, flatY
	flat := weather.SampleAt(wa.sim, q)
	coldBias := md.RegionBiasAt(coldX, coldY)
	flatBias := md.RegionBiasAt(flatX, flatY)
	if coldBias.Temp >= 0 || coldBias.Fog <= 0 {
		t.Fatalf("寒冷区域偏置应生效: %+v", coldBias)
	}
	if flatBias.Temp != 0 || flatBias.Fog != 0 || flatBias.Rain != 0 {
		t.Fatalf("草原应无天气偏置: %+v", flatBias)
	}
	if cold.Temperature >= flat.Temperature {
		t.Fatalf("寒冷区域采样应更冷: cold=%.1f flat=%.1f", cold.Temperature, flat.Temperature)
	}
}

// 存档往返：区域 id + 天气基值随档保存/恢复。
func TestSaveLoadRegions(t *testing.T) {
	mapPath, biomesPath := regionTestPaths(t)
	wa := NewWorldActor(WorldConfig{MapPath: mapPath, BiomesPath: biomesPath, MapSeed: 42})
	md := ecs.Resource[worldmap.MapData](wa.sim)
	data := wa.Save()

	wa2 := NewWorldActor(WorldConfig{})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}
	md2 := ecs.Resource[worldmap.MapData](wa2.sim)
	if len(md2.RegionIDs) != len(md.RegionIDs) || len(md2.RegionWeather) != len(md.RegionWeather) {
		t.Fatalf("区域数据未恢复: ids=%d/%d weather=%d/%d",
			len(md2.RegionIDs), len(md.RegionIDs), len(md2.RegionWeather), len(md.RegionWeather))
	}
	for i := range md.RegionIDs {
		if md.RegionIDs[i] != md2.RegionIDs[i] {
			t.Fatal("区域 id 表不一致")
		}
	}
	if len(md2.RegionBiomes) != len(md.RegionBiomes) {
		t.Fatalf("biome 映射未恢复: %d/%d", len(md2.RegionBiomes), len(md.RegionBiomes))
	}
	for y := 0; y < md.Height; y++ {
		for x := 0; x < md.Width; x++ {
			if md.BiomeAt(x, y) != md2.BiomeAt(x, y) {
				t.Fatalf("BiomeAt(%d,%d) 往返不一致: %v/%v", x, y, md.BiomeAt(x, y), md2.BiomeAt(x, y))
			}
		}
	}
}

func TestOldSaveBiomeMappingFallsBackToGeneratedMap(t *testing.T) {
	mapPath, biomesPath := regionTestPaths(t)
	cfg := WorldConfig{MapPath: mapPath, BiomesPath: biomesPath, MapSeed: 42}
	original := NewWorldActor(cfg)
	var save game.SaveData
	if err := pb.Unmarshal(original.Save(), &save); err != nil {
		t.Fatal(err)
	}
	save.RegionBiomes = nil // 模拟新增字段之前的旧档
	data, err := pb.Marshal(&save)
	if err != nil {
		t.Fatal(err)
	}
	loaded := NewWorldActor(cfg)
	if err := loaded.Load(data); err != nil {
		t.Fatal(err)
	}
	want := ecs.Resource[worldmap.MapData](original.sim)
	got := ecs.Resource[worldmap.MapData](loaded.sim)
	if got.BiomeAt(want.SpawnX, want.SpawnY) != want.BiomeAt(want.SpawnX, want.SpawnY) {
		t.Fatalf("旧档 biome 回退失败: got=%v want=%v",
			got.BiomeAt(want.SpawnX, want.SpawnY), want.BiomeAt(want.SpawnX, want.SpawnY))
	}
}

// 真实配置文件冒烟：configs/map.json + biomes.json 能生成区域且全图连通。
func TestRealConfigsRegionLayout(t *testing.T) {
	spec, err := worldmap.LoadMapSpec("../../../configs/map.json")
	if err != nil {
		t.Fatal(err)
	}
	bs, err := worldmap.LoadBiomes("../../../configs/biomes.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(bs[worldmap.BiomeSwamp].Drops) == 0 || len(bs[worldmap.BiomeMine].Drops) == 0 {
		t.Fatal("真实 swamp/mine 配置都必须包含可触发的 biome 附加掉落")
	}
	res := worldmap.NewMapGenerator(42, spec, bs).Generate()
	if len(res.Regions) == 0 {
		t.Fatal("真实配置应生成区域")
	}
	woodInSwamp, flintInMine := false, false
	for _, resource := range res.Resources {
		biome := game.BiomeType_BIOME_TYPE_UNSPECIFIED
		id := int(res.RegionIDs[resource.Y*res.Width+resource.X])
		if id > 0 && id <= len(res.RegionBiomes) {
			biome = res.RegionBiomes[id-1]
		}
		walkable := game.TerrainType(res.TileTypes[resource.Y*res.Width+resource.X]) != game.TerrainType_TERRAIN_TYPE_WATER
		woodInSwamp = woodInSwamp || walkable && biome == worldmap.BiomeSwamp && resource.Kind == components.ItemWood
		flintInMine = flintInMine || walkable && biome == worldmap.BiomeMine && resource.Kind == components.ItemFlint
	}
	if !woodInSwamp || !flintInMine {
		t.Fatalf("真实地图必须能触发两类 biome 掉落: swamp wood=%v mine flint=%v", woodInSwamp, flintInMine)
	}
	if !worldmap.ValidateRegionConnectivity(res.RegionIDs, res.TileTypes, res.Width, res.Height, res.SpawnX, res.SpawnY) {
		for i, r := range res.Regions {
			tiles, water := 0, 0
			for idx, id := range res.RegionIDs {
				if int(id) == i+1 {
					tiles++
					if res.TileTypes[idx] == byte(game.TerrainType_TERRAIN_TYPE_WATER) {
						water++
					}
				}
			}
			t.Logf("region %s(%s) center=(%d,%d) tiles=%d water=%d", r.ID, r.Biome, r.X, r.Y, tiles, water)
		}
		t.Fatal("真实配置生成的区域未全部连通")
	}
}
