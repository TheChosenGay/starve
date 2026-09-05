package worldmap

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"

	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// MapSpec 地图规格（configs/map.json）：尺寸 + 出生点 + 手摆 + 撒点 + 高度参数。
// 手摆保证开局可玩；撒点按 seed 确定性生成；高度场是静态地形。
type MapSpec struct {
	Width   int        `json:"width"`
	Height  int        `json:"height"`
	SpawnX  int        `json:"spawn_x"`
	SpawnY  int        `json:"spawn_y"`
	Terrain HeightSpec `json:"terrain"`

	RegionLayout *RegionLayoutSpec `json:"region_layout"` // 区域布局（可省略：回退全局地形）
	Handplaced   HandplacedSpec    `json:"handplaced"`
	Scatter      []ScatterRule     `json:"scatter"`
}

type HeightSpec struct {
	Hills           int `json:"hills"`             // 山丘数量
	MaxAmp          int `json:"max_amp"`           // 单丘最大高度
	RockLevel       int `json:"rock_level"`        // 高度 ≥ 此值为岩石
	WaterLevel      int `json:"water_level"`       // 高度 ≤ 此值为水
	SpawnFlatRadius int `json:"spawn_flat_radius"` // 出生点压平半径
	BaseLevel       int `json:"base_level"`        // 基底海拔：全图高度加常数（默认 0）
}

type HandplacedSpec struct {
	Resources      []ResourceSeed      `json:"resources"`
	Stations       []StationSeed       `json:"stations"`
	RevivalStatues []RevivalStatueSeed `json:"revival_statues"`
	Loot           []LootSeed          `json:"loot"`
	EffectTiles    []EffectTileSeed    `json:"effect_tiles"` // 手摆地块效果（毒沼等）
	Emitters       []EmitterSeed       `json:"emitters"`     // 手摆效果发射器实体（增益植物/火堆）
	Creatures      []CreatureSeed      `json:"creatures"`    // 手摆生物（测试/开局）
}

type RevivalStatueSeed struct {
	X             int   `json:"x"`
	Y             int   `json:"y"`
	Uses          int   `json:"uses"`
	DurationTicks int64 `json:"duration_ticks"`
}

// CreatureSeed 手摆生物：kind（配置名）+ 坐标。
type CreatureSeed struct {
	Kind string `json:"kind"`
	X    int    `json:"x"`
	Y    int    `json:"y"`
}

// LootSeed 初始可拾取物资（开局"靠捡东西"）。
type LootSeed struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
}

// EffectTileSeed 手摆地块效果：effect 为配置名（见 components.EffectOrderByName），
// 覆盖该格地形派生的效果。如毒沼、圣坛、陷阱。
type EffectTileSeed struct {
	Effect string `json:"effect"`
	Param  int    `json:"param"` // 效果强度（如毒伤量；0=默认）
	X      int    `json:"x"`
	Y      int    `json:"y"`
}

// EffectInstanceSeed 配置里的一个效果实例：order 为配置名，param 为强度。
type EffectInstanceSeed struct {
	Order string `json:"order"`
	Param int    `json:"param"`
}

// EmitterSeed 手摆效果发射器实体（增益植物/火堆等）：效果集合 + 半径。
// 生成时转成 Position + EffectEmitter 组件（快照/存档随实体走）。
type EmitterSeed struct {
	Effects []EffectInstanceSeed `json:"effects"`
	Radius  int                  `json:"radius"`
	X       int                  `json:"x"`
	Y       int                  `json:"y"`
}

type ScatterRule struct {
	Kind    string `json:"kind"`
	Action  string `json:"action"`
	Work    int    `json:"work"`
	Count   int    `json:"count"`
	MinDist int    `json:"min_dist"`
}

// MapResult 生成结果：地形场 + 实体（资源/工作站/初始物资）。
type MapResult struct {
	Width, Height  int
	SpawnX, SpawnY int
	CornerHeights  []byte // (W+1)×(H+1)，行优先
	CornerTypes    []byte // 同上，TerrainType 枚举值
	TileTypes      []byte // W×H，每格 TerrainType（服务端内部：连通校验/天气）
	TileEffects    []byte // W×H，每格 EffectOrder（0=无；服务端内部，不下发）
	TileParams     []int8 // W×H，每格效果参数（有符号；0=默认；服务端内部，不下发）
	RegionIDs      []byte // W×H，每格区域实例 id（1-based；0=未分配；服务端内部）
	Regions        []RegionInstance
	RegionBiomes   []BiomeType   // 索引 0 对应区域实例 id 1
	RegionWeather  []WeatherBias // 区域天气基值（索引 = 区域实例 id）
	Resources      []SeededResource
	Stations       []StationSeed
	RevivalStatues []RevivalStatueSeed
	Loot           []LootSeed
	Emitters       []EmitterSeed
	Creatures      []CreatureSeed
}

// LoadMapSpec 解析 map.json（尺寸/出生点/手摆/撒点/高度参数）。
func LoadMapSpec(path string) (*MapSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec MapSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	for i := range spec.Handplaced.RevivalStatues {
		statue := &spec.Handplaced.RevivalStatues[i]
		if statue.Uses == 0 {
			statue.Uses = components.DefaultHauntUses
		}
		if statue.DurationTicks == 0 {
			statue.DurationTicks = components.DefaultHauntDuration
		}
	}
	if spec.Width <= 0 || spec.Height <= 0 {
		return nil, fmt.Errorf("map size must be > 0")
	}
	if err := validateMapSpec(&spec); err != nil {
		return nil, err
	}
	if spec.Terrain.Hills <= 0 {
		spec.Terrain.Hills = 8
	}
	if spec.Terrain.MaxAmp <= 0 {
		spec.Terrain.MaxAmp = 6
	}
	if spec.Terrain.RockLevel <= 0 {
		spec.Terrain.RockLevel = 6
	}
	if spec.Terrain.SpawnFlatRadius <= 0 {
		spec.Terrain.SpawnFlatRadius = 6
	}
	return &spec, nil
}

func validateMapSpec(spec *MapSpec) error {
	inBounds := func(x, y int) bool {
		return x >= 0 && x < spec.Width && y >= 0 && y < spec.Height
	}
	if !inBounds(spec.SpawnX, spec.SpawnY) {
		return fmt.Errorf("spawn (%d,%d) outside map %dx%d", spec.SpawnX, spec.SpawnY, spec.Width, spec.Height)
	}
	for i, resource := range spec.Handplaced.Resources {
		if _, _, err := ResolveResourceSpec(resource.Kind, resource.Action, resource.Work); err != nil {
			return fmt.Errorf("handplaced.resources[%d]: %w", i, err)
		}
		if !inBounds(resource.X, resource.Y) {
			return fmt.Errorf("handplaced.resources[%d]: position (%d,%d) outside map", i, resource.X, resource.Y)
		}
	}
	for i, station := range spec.Handplaced.Stations {
		if _, ok := components.WorkstationTypeByName[station.Type]; !ok {
			return fmt.Errorf("handplaced.stations[%d]: unknown type %q", i, station.Type)
		}
		if !inBounds(station.X, station.Y) {
			return fmt.Errorf("handplaced.stations[%d]: position (%d,%d) outside map", i, station.X, station.Y)
		}
	}
	for i, statue := range spec.Handplaced.RevivalStatues {
		if !inBounds(statue.X, statue.Y) {
			return fmt.Errorf("handplaced.revival_statues[%d]: position (%d,%d) outside map", i, statue.X, statue.Y)
		}
		if statue.X == spec.SpawnX && statue.Y == spec.SpawnY {
			return fmt.Errorf("handplaced.revival_statues[%d]: cannot cover spawn", i)
		}
		if statue.Uses <= 0 || statue.DurationTicks <= 0 {
			return fmt.Errorf("handplaced.revival_statues[%d]: uses and duration_ticks must be > 0", i)
		}
	}
	for i, loot := range spec.Handplaced.Loot {
		if _, ok := components.ItemKindByName[loot.Kind]; !ok {
			return fmt.Errorf("handplaced.loot[%d]: unknown kind %q", i, loot.Kind)
		}
		if loot.Count <= 0 {
			return fmt.Errorf("handplaced.loot[%d]: count must be > 0", i)
		}
		if !inBounds(loot.X, loot.Y) {
			return fmt.Errorf("handplaced.loot[%d]: position (%d,%d) outside map", i, loot.X, loot.Y)
		}
	}
	for i, creature := range spec.Handplaced.Creatures {
		if _, ok := components.CreatureKindByName[creature.Kind]; !ok {
			return fmt.Errorf("handplaced.creatures[%d]: unknown kind %q", i, creature.Kind)
		}
		if !inBounds(creature.X, creature.Y) {
			return fmt.Errorf("handplaced.creatures[%d]: position (%d,%d) outside map", i, creature.X, creature.Y)
		}
	}
	for i, tile := range spec.Handplaced.EffectTiles {
		if _, ok := components.EffectOrderByName[tile.Effect]; !ok {
			return fmt.Errorf("handplaced.effect_tiles[%d]: unknown effect %q", i, tile.Effect)
		}
		if !inBounds(tile.X, tile.Y) {
			return fmt.Errorf("handplaced.effect_tiles[%d]: position (%d,%d) outside map", i, tile.X, tile.Y)
		}
	}
	for i, emitter := range spec.Handplaced.Emitters {
		if emitter.Radius < 0 || len(emitter.Effects) == 0 {
			return fmt.Errorf("handplaced.emitters[%d]: radius must be >= 0 and effects cannot be empty", i)
		}
		if !inBounds(emitter.X, emitter.Y) {
			return fmt.Errorf("handplaced.emitters[%d]: position (%d,%d) outside map", i, emitter.X, emitter.Y)
		}
		for j, effect := range emitter.Effects {
			if _, ok := components.EffectOrderByName[effect.Order]; !ok {
				return fmt.Errorf("handplaced.emitters[%d].effects[%d]: unknown order %q", i, j, effect.Order)
			}
		}
	}
	for i, rule := range spec.Scatter {
		if _, _, err := ResolveResourceSpec(rule.Kind, rule.Action, rule.Work); err != nil {
			return fmt.Errorf("scatter[%d]: %w", i, err)
		}
		if rule.Count < 0 || rule.MinDist < 0 {
			return fmt.Errorf("scatter[%d]: count and min_dist must be >= 0", i)
		}
	}
	return nil
}

// MapGenerator 确定性地图生成器：seed + spec → MapResult（纯函数）。
type MapGenerator struct {
	seed   uint64
	spec   *MapSpec
	biomes map[BiomeType]BiomeSpec
	rng    *rand.Rand
}

// NewMapGenerator 构造生成器（seed + 规格 + 生物群系表）。
func NewMapGenerator(seed uint64, spec *MapSpec, biomes map[BiomeType]BiomeSpec) *MapGenerator {
	return &MapGenerator{seed: seed, spec: spec, biomes: biomes}
}

// Generate 生成地形高度场 + 撒点实体。确定性：唯一随机源 = rng（seed 化）。
func (g *MapGenerator) Generate() *MapResult {
	w, h := g.spec.Width, g.spec.Height
	res := &MapResult{
		Width:  w,
		Height: h,
		SpawnX: g.spec.SpawnX,
		SpawnY: g.spec.SpawnY,
	}
	if g.rng == nil {
		g.rng = rand.New(rand.NewSource(int64(g.seed)))
	}

	g.genHeightField(res)
	if g.spec.RegionLayout != nil && len(g.biomes) > 0 {
		// 区域模式：偏好放置 + 区域地形合成 + 连通校验（失败换 seed 偏移重试）
		var regions []RegionInstance
		var ids []byte
		for attempt := 0; attempt < 32; attempt++ {
			regions, ids = g.placeRegions(g.spec.RegionLayout, g.biomes, attempt, res)
			g.genRegionTerrain(res, regions, ids)
			if ValidateRegionConnectivity(ids, res.TileTypes, w, h, res.SpawnX, res.SpawnY) {
				break
			}
		}
		res.RegionIDs = ids
		res.Regions = regions
		res.RegionBiomes = regionBiomesOf(regions)
		res.RegionWeather = regionWeatherOf(regions, g.biomes)
		g.genTileEffects(res)
		g.genHandplaced(res)
		g.genRegionResources(res, regions, ids)
	} else {
		// 回退：全局地形映射（无 region_layout 的旧配置）
		g.genFallbackTerrain(res)
		g.genTileEffects(res)
		g.genHandplaced(res)
		g.genScatter(res)
	}
	return res
}

// genHeightField 高度场：随机山丘叠加 → 出生点压平 → 相邻差 ≤1 → 类型映射。
func (g *MapGenerator) genHeightField(res *MapResult) {
	w, h := res.Width, res.Height
	cw, ch := w+1, h+1
	heights := make([]float64, cw*ch)

	// 随机山丘（seed 驱动，确定性）
	hills := g.spec.Terrain.Hills
	centers := make([][3]float64, 0, hills) // cx, cy, r
	amps := make([]float64, 0, hills)
	for i := 0; i < hills; i++ {
		cx := float64(g.rng.Intn(w + 1))
		cy := float64(g.rng.Intn(h + 1))
		r := 3 + float64(g.rng.Intn(7))                       // 3..9
		amp := 1 + float64(g.rng.Intn(g.spec.Terrain.MaxAmp)) // 1..MaxAmp
		centers = append(centers, [3]float64{cx, cy, r})
		amps = append(amps, amp)
	}
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			v := 0.0
			for i := range centers {
				dx := float64(x) - centers[i][0]
				dy := float64(y) - centers[i][1]
				d2 := dx*dx + dy*dy
				r2 := centers[i][2] * centers[i][2]
				v += amps[i] * math.Exp(-d2/(2*r2))
			}
			heights[y*cw+x] = v
		}
	}

	// 出生点压平
	r := g.spec.Terrain.SpawnFlatRadius
	pinned := make([]bool, cw*ch)
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			dx := float64(x - res.SpawnX)
			dy := float64(y - res.SpawnY)
			if dx*dx+dy*dy <= float64(r*r) {
				heights[y*cw+x] = 0
				pinned[y*cw+x] = true
			}
		}
	}

	// 相邻差 ≤ 1（确定性松弛：正反两轮扫描）
	ints := make([]int, cw*ch)
	for i, v := range heights {
		ints[i] = int(math.Round(v)) + g.spec.Terrain.BaseLevel
		if ints[i] < 0 {
			ints[i] = 0
		}
	}
	for pass := 0; pass < 4; pass++ {
		for y := 0; y < ch; y++ {
			for x := 0; x < cw; x++ {
				i := y*cw + x
				if pinned[i] {
					continue
				}
				if x > 0 {
					ints[i] = clampAdj(ints[i], ints[i-1])
				}
				if y > 0 {
					ints[i] = clampAdj(ints[i], ints[i-cw])
				}
			}
		}
		for y := ch - 1; y >= 0; y-- {
			for x := cw - 1; x >= 0; x-- {
				i := y*cw + x
				if pinned[i] {
					continue
				}
				if x < cw-1 {
					ints[i] = clampAdj(ints[i], ints[i+1])
				}
				if y < ch-1 {
					ints[i] = clampAdj(ints[i], ints[i+cw])
				}
			}
		}
	}

	res.CornerHeights = make([]byte, cw*ch)
	for i, v := range ints {
		res.CornerHeights[i] = byte(v)
	}
}

// clampAdj 把 v 压到与邻居差 ≤1（向邻居靠拢）。
func clampAdj(v, neighbor int) int {
	if v > neighbor+1 {
		return neighbor + 1
	}
	if v < neighbor-1 {
		return neighbor - 1
	}
	return v
}

// RegionTileType 按区域 terrain 规则（或无区域时的全局规则）把高度映射成地形。
// 出生点压平区强制草地（安全区）。
func (g *MapGenerator) RegionTileType(h, x, y int, res *MapResult, regions []RegionInstance, ids []byte) game.TerrainType {
	dx := x - res.SpawnX
	dy := y - res.SpawnY
	r := g.spec.Terrain.SpawnFlatRadius
	if dx*dx+dy*dy <= r*r {
		return game.TerrainType_TERRAIN_TYPE_GRASS
	}
	waterLv, rockLv := g.spec.Terrain.WaterLevel, g.spec.Terrain.RockLevel
	snowLv := rockLv + 2
	if regions != nil && ids != nil {
		id := int(ids[y*res.Width+x])
		if id > 0 && id <= len(regions) {
			if b, ok := g.biomes[regions[id-1].Biome]; ok {
				t := b.Terrain
				if t.WaterLevel != 0 {
					waterLv = t.WaterLevel
				}
				if t.RockLevel != 0 {
					rockLv = t.RockLevel
				}
				snowLv = t.SnowLevel
				if snowLv == 0 {
					snowLv = rockLv + 2
				}
			}
		}
	}
	switch {
	case h <= waterLv:
		return game.TerrainType_TERRAIN_TYPE_WATER
	case h == waterLv+1:
		return game.TerrainType_TERRAIN_TYPE_SAND
	case h >= snowLv:
		return game.TerrainType_TERRAIN_TYPE_SNOW // 高山雪线（效果：减速）
	case h >= rockLv:
		return game.TerrainType_TERRAIN_TYPE_ROCK
	default:
		return game.TerrainType_TERRAIN_TYPE_GRASS
	}
}

// genRegionTerrain 按区域 terrain 规则把高度映射成每格地形（W×H），再展开到角。
// 角 (x,y) 取格子 (x,y) 的类型（右/下边沿用边界格），保证区域边界平滑过渡。
func (g *MapGenerator) genRegionTerrain(res *MapResult, regions []RegionInstance, ids []byte) {
	w, h := res.Width, res.Height
	tiles := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cornerH := int(res.CornerHeights[y*(w+1)+x])
			terrain := g.RegionTileType(cornerH, x, y, res, regions, ids)
			id := int(ids[y*w+x])
			if id > 0 && id <= len(regions) {
				region := regions[id-1]
				if biome, ok := g.biomes[region.Biome]; ok &&
					biomeCorridorContains(region, biome.CorridorWidth, id-1, x, y) {
					terrain = game.TerrainType_TERRAIN_TYPE_GRASS
				}
			}
			tiles[y*w+x] = byte(terrain)
		}
	}
	res.TileTypes = tiles
	res.CornerTypes = make([]byte, (w+1)*(h+1))
	for y := 0; y <= h; y++ {
		for x := 0; x <= w; x++ {
			res.CornerTypes[y*(w+1)+x] = tiles[clampInt(y, 0, h-1)*w+clampInt(x, 0, w-1)]
		}
	}
}

// genFallbackTerrain 无区域布局时的全局地形映射（兼容旧配置）。
func (g *MapGenerator) genFallbackTerrain(res *MapResult) {
	w, h := res.Width, res.Height
	cw, ch := w+1, h+1
	tiles := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cornerH := int(res.CornerHeights[y*cw+x])
			tiles[y*w+x] = byte(g.RegionTileType(cornerH, x, y, res, nil, nil))
		}
	}
	res.TileTypes = tiles
	res.CornerTypes = make([]byte, cw*ch)
	for y := 0; y < ch; y++ {
		for x := 0; x < cw; x++ {
			cornerH := int(res.CornerHeights[y*cw+x])
			res.CornerTypes[y*cw+x] = byte(g.RegionTileType(cornerH, x, y, res, nil, nil))
		}
	}
}

// genTileEffects 地块效果表：地形派生（水→毒、雪→减速）+ 手摆覆盖。
// 确定性：纯函数，无随机；手摆优先于地形派生（可把某格改成其他效果）。
func (g *MapGenerator) genTileEffects(res *MapResult) {
	w, h := res.Width, res.Height
	res.TileEffects = make([]byte, w*h)
	res.TileParams = make([]int8, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// 取左上角的地形类型作为整格类型（简单确定，后续可细化四角混合）
			typ := game.TerrainType(res.CornerTypes[y*(w+1)+x])
			switch typ {
			case game.TerrainType_TERRAIN_TYPE_WATER:
				res.TileEffects[y*w+x] = byte(components.EffectPoison)
			case game.TerrainType_TERRAIN_TYPE_SNOW:
				res.TileEffects[y*w+x] = byte(components.EffectSpeed)
				res.TileParams[y*w+x] = -50 // 雪地减速
			}
		}
	}
	// 区域效果：按 biome tile_effects 覆盖率铺（覆盖地形派生效果）
	if len(res.RegionIDs) == w*h && len(res.Regions) > 0 {
		regionRng := rand.New(rand.NewSource(int64(g.seed) + 0x1F2E3D4C))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				id := int(res.RegionIDs[y*w+x])
				if id <= 0 || id > len(res.Regions) {
					continue
				}
				b, ok := g.biomes[res.Regions[id-1].Biome]
				if !ok {
					continue
				}
				for _, te := range b.TileEffects {
					o, ok := components.EffectOrderByName[te.Effect]
					if !ok || regionRng.Float32() >= te.Coverage {
						continue
					}
					res.TileEffects[y*w+x] = byte(o)
					res.TileParams[y*w+x] = int8(te.Param)
				}
			}
		}
	}
	for _, s := range g.spec.Handplaced.EffectTiles {
		o, ok := components.EffectOrderByName[s.Effect]
		if !ok || s.X < 0 || s.Y < 0 || s.X >= w || s.Y >= h {
			continue
		}
		res.TileEffects[s.Y*w+s.X] = byte(o)
		res.TileParams[s.Y*w+s.X] = int8(s.Param)
	}
}

type resourcePos struct{ x, y int }

// genRegionResources 按区域规则生成均匀底层 + 局部聚簇，并为配置了通道的区域留白。
// 全部候选共享 occupied，保证不同种类之间也遵守最小间距。
func (g *MapGenerator) genRegionResources(res *MapResult, regions []RegionInstance, ids []byte) {
	var occupied []resourcePos
	for _, r := range res.Resources {
		occupied = append(occupied, resourcePos{r.X, r.Y})
	}
	for _, s := range res.Stations {
		occupied = append(occupied, resourcePos{s.X, s.Y})
	}
	for _, s := range res.RevivalStatues {
		occupied = append(occupied, resourcePos{s.X, s.Y})
	}
	for _, l := range res.Loot {
		occupied = append(occupied, resourcePos{l.X, l.Y})
	}
	for ri, r := range regions {
		b, ok := g.biomes[r.Biome]
		if !ok {
			continue
		}
		for _, br := range b.Resources {
			kind, action, err := ResolveResourceSpec(br.Kind, br.Action, br.Work)
			if err != nil {
				continue
			}
			place := func(x, y int) bool {
				if !resourceCandidateAllowed(res, ids, ri, r, b, br, occupied, x, y) {
					return false
				}
				res.Resources = append(res.Resources, SeededResource{Kind: kind, X: x, Y: y, Action: action, Work: br.Work})
				occupied = append(occupied, resourcePos{x, y})
				return true
			}

			placed := 0
			for attempts := 0; placed < br.Density && attempts < max(200, br.Density*80); attempts++ {
				x := g.rng.Intn(res.Width)
				y := g.rng.Intn(res.Height)
				if place(x, y) {
					placed++
				}
			}

			var clusterCenters []resourcePos
			for cluster := 0; cluster < br.Clusters; cluster++ {
				cx, cy, found := 0, 0, false
				for attempts := 0; attempts < 300; attempts++ {
					cx = g.rng.Intn(res.Width)
					cy = g.rng.Intn(res.Height)
					if !resourceTileAllowed(res, ids, ri, r, b, br, cx, cy) {
						continue
					}
					separated := true
					for _, center := range clusterCenters {
						dx, dy := cx-center.x, cy-center.y
						if dx*dx+dy*dy < 4*br.ClusterRadius*br.ClusterRadius {
							separated = false
							break
						}
					}
					if separated {
						found = true
						break
					}
				}
				if !found {
					continue
				}
				clusterCenters = append(clusterCenters, resourcePos{cx, cy})
				clusterPlaced := 0
				for attempts := 0; clusterPlaced < br.ClusterDensity &&
					attempts < max(200, br.ClusterDensity*100); attempts++ {
					dx := g.rng.Intn(br.ClusterRadius*2+1) - br.ClusterRadius
					dy := g.rng.Intn(br.ClusterRadius*2+1) - br.ClusterRadius
					if dx*dx+dy*dy > br.ClusterRadius*br.ClusterRadius {
						continue
					}
					if place(cx+dx, cy+dy) {
						clusterPlaced++
					}
				}
			}
		}
	}
}

func resourceCandidateAllowed(
	res *MapResult,
	ids []byte,
	regionIndex int,
	region RegionInstance,
	biome BiomeSpec,
	rule BiomeResource,
	occupied []resourcePos,
	x, y int,
) bool {
	if !resourceTileAllowed(res, ids, regionIndex, region, biome, rule, x, y) {
		return false
	}
	for _, p := range occupied {
		if abs(x-p.x)+abs(y-p.y) < rule.MinDist {
			return false
		}
	}
	return true
}

func resourceTileAllowed(
	res *MapResult,
	ids []byte,
	regionIndex int,
	region RegionInstance,
	biome BiomeSpec,
	rule BiomeResource,
	x, y int,
) bool {
	if x < 0 || y < 0 || x >= res.Width || y >= res.Height {
		return false
	}
	if ids[y*res.Width+x] != byte(regionIndex+1) {
		return false
	}
	// 程序化资源避开出生点安全圈；手摆资源仍可用于开局引导。
	if abs(x-res.SpawnX)+abs(y-res.SpawnY) <= 3 {
		return false
	}
	if biomeCorridorContains(region, biome.CorridorWidth, regionIndex, x, y) {
		return false
	}
	terrain := game.TerrainType(res.TileTypes[y*res.Width+x])
	if len(rule.Terrains) == 0 {
		return terrain != game.TerrainType_TERRAIN_TYPE_WATER
	}
	for _, name := range rule.Terrains {
		if terrainTypeByName[name] == terrain {
			return true
		}
	}
	return false
}

// biomeCorridorContains 以区域中心为轴留一条贯穿区域的草地通道。
// 方向由区域序号稳定选择（横/竖/两条对角线），不依赖撒点随机数消费顺序。
func biomeCorridorContains(region RegionInstance, halfWidth, regionIndex, x, y int) bool {
	if halfWidth <= 0 {
		return false
	}
	dx, dy := x-region.X, y-region.Y
	switch regionIndex % 4 {
	case 0:
		return abs(dy) <= halfWidth
	case 1:
		return abs(dx) <= halfWidth
	case 2:
		return abs(dx-dy) <= halfWidth
	default:
		return abs(dx+dy) <= halfWidth
	}
}

// genHandplaced 手摆区：资源/工作站/初始物资直接放置。
func (g *MapGenerator) genHandplaced(res *MapResult) {
	for _, s := range g.spec.Handplaced.Resources {
		kind, action, err := ResolveResourceSpec(s.Kind, s.Action, s.Work)
		if err != nil {
			continue
		}
		res.Resources = append(res.Resources, SeededResource{Kind: kind, X: s.X, Y: s.Y, Action: action, Work: s.Work})
	}
	res.Stations = append(res.Stations, g.spec.Handplaced.Stations...)
	res.RevivalStatues = append(res.RevivalStatues, g.spec.Handplaced.RevivalStatues...)
	res.Emitters = append(res.Emitters, g.spec.Handplaced.Emitters...)
	res.Creatures = append(res.Creatures, g.spec.Handplaced.Creatures...)
	for _, l := range g.spec.Handplaced.Loot {
		k, ok := components.ItemKindByName[l.Kind]
		if !ok || l.Count <= 0 {
			continue
		}
		res.Loot = append(res.Loot, LootSeed{Kind: l.Kind, Count: l.Count, X: l.X, Y: l.Y})
		_ = k
	}
}

// genScatter 程序化撒点：seed 伪随机 + min_dist 冲突检测（确定性尝试）。
func (g *MapGenerator) genScatter(res *MapResult) {
	// 已占位置（手摆资源 + 工作站 + 物资）用于冲突检测
	type pos struct{ x, y int }
	occupied := make([]pos, 0, 32)
	for _, r := range res.Resources {
		occupied = append(occupied, pos{r.X, r.Y})
	}
	for _, s := range res.Stations {
		occupied = append(occupied, pos{s.X, s.Y})
	}
	for _, s := range res.RevivalStatues {
		occupied = append(occupied, pos{s.X, s.Y})
	}
	for _, l := range res.Loot {
		occupied = append(occupied, pos{l.X, l.Y})
	}

	for _, rule := range g.spec.Scatter {
		kind, action, err := ResolveResourceSpec(rule.Kind, rule.Action, rule.Work)
		if err != nil {
			continue
		}
		if rule.MinDist <= 0 {
			continue
		}
		placed := 0
		for attempts := 0; placed < rule.Count && attempts < rule.Count*30; attempts++ {
			x := g.rng.Intn(res.Width)
			y := g.rng.Intn(res.Height)
			// 出生点安全区（曼哈顿 ≤ 3）：阻挡类资源（树/岩）不堵出生位
			if abs(x-g.spec.SpawnX)+abs(y-g.spec.SpawnY) <= 3 {
				continue
			}
			ok := true
			for _, p := range occupied {
				if abs(x-p.x)+abs(y-p.y) < rule.MinDist {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			res.Resources = append(res.Resources, SeededResource{Kind: kind, X: x, Y: y, Action: action, Work: rule.Work})
			occupied = append(occupied, pos{x, y})
			placed++
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ToProto 把生成结果编码成端上契约（MapConfig）。
func (r *MapResult) ToProto() *game.MapConfig {
	return &game.MapConfig{
		Width:         int32(r.Width),
		Height:        int32(r.Height),
		CornerHeights: r.CornerHeights,
		CornerTypes:   r.CornerTypes,
		SpawnX:        int32(r.SpawnX),
		SpawnY:        int32(r.SpawnY),
	}
}
