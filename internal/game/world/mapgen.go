package world

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"

	"starve/internal/ecs"
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

	Handplaced HandplacedSpec `json:"handplaced"`
	Scatter    []ScatterRule  `json:"scatter"`
}

type HeightSpec struct {
	Hills           int `json:"hills"`             // 山丘数量
	MaxAmp          int `json:"max_amp"`           // 单丘最大高度
	RockLevel       int `json:"rock_level"`        // 高度 ≥ 此值为岩石
	WaterLevel      int `json:"water_level"`       // 高度 ≤ 此值为水
	SpawnFlatRadius int `json:"spawn_flat_radius"` // 出生点压平半径
}

type HandplacedSpec struct {
	Resources   []ResourceSeed   `json:"resources"`
	Stations    []StationSeed    `json:"stations"`
	Loot        []LootSeed       `json:"loot"`
	EffectTiles []EffectTileSeed `json:"effect_tiles"` // 手摆地块效果（毒沼等）
	Emitters    []EmitterSeed    `json:"emitters"`     // 手摆效果发射器实体（增益植物/火堆）
}

// LootSeed 初始可拾取物资（开局"靠捡东西"）。
type LootSeed struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
}

// EffectTileSeed 手摆地块效果：effect 为配置名（见 effectOrderByName），
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
	TileEffects    []byte // W×H，每格 EffectOrder（0=无；服务端内部，不下发）
	TileParams     []int8 // W×H，每格效果参数（有符号；0=默认；服务端内部，不下发）
	Resources      []seededResource
	Stations       []StationSeed
	Loot           []LootSeed
	Emitters       []EmitterSeed
}

// loadMapSpec 解析 map.json（尺寸/出生点/手摆/撒点/高度参数）。
func loadMapSpec(path string) (*MapSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var spec MapSpec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	if spec.Width <= 0 || spec.Height <= 0 {
		return nil, fmt.Errorf("map size must be > 0")
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

// MapGenerator 确定性地图生成器：seed + spec → MapResult（纯函数）。
type MapGenerator struct {
	seed uint64
	spec *MapSpec
	rng  *rand.Rand
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
	g.genTileEffects(res)
	g.genHandplaced(res)
	g.genScatter(res)
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
		ints[i] = int(math.Round(v))
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
	res.CornerTypes = make([]byte, cw*ch)
	for i, v := range ints {
		res.CornerHeights[i] = byte(v)
		res.CornerTypes[i] = byte(g.typeAt(v, i, cw, res))
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

// typeAt 类型映射：出生点压平区强制草地；其余按高度。
func (g *MapGenerator) typeAt(h, i, cw int, res *MapResult) game.TerrainType {
	x, y := i%cw, i/cw
	dx := x - res.SpawnX
	dy := y - res.SpawnY
	r := g.spec.Terrain.SpawnFlatRadius
	if dx*dx+dy*dy <= r*r {
		return game.TerrainType_TERRAIN_TYPE_GRASS
	}
	switch {
	case h <= g.spec.Terrain.WaterLevel:
		return game.TerrainType_TERRAIN_TYPE_WATER
	case h == 2:
		return game.TerrainType_TERRAIN_TYPE_SAND
	case h >= g.spec.Terrain.RockLevel+2:
		return game.TerrainType_TERRAIN_TYPE_SNOW // 高山雪线（效果：减速）
	case h >= g.spec.Terrain.RockLevel:
		return game.TerrainType_TERRAIN_TYPE_ROCK
	default:
		return game.TerrainType_TERRAIN_TYPE_GRASS
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
	for _, s := range g.spec.Handplaced.EffectTiles {
		o, ok := effectOrderByName[s.Effect]
		if !ok || s.X < 0 || s.Y < 0 || s.X >= w || s.Y >= h {
			continue
		}
		res.TileEffects[s.Y*w+s.X] = byte(o)
		res.TileParams[s.Y*w+s.X] = int8(s.Param)
	}
}

// effectOrderByName 配置字符串 → 效果枚举（新效果 = 枚举值 + 这里加一行 + 组件实现）。
var effectOrderByName = map[string]components.EffectOrder{
	"speed":  components.EffectSpeed,
	"poison": components.EffectPoison,
}

// genHandplaced 手摆区：资源/工作站/初始物资直接放置。
func (g *MapGenerator) genHandplaced(res *MapResult) {
	for _, s := range g.spec.Handplaced.Resources {
		k, ok := itemKindByName[s.Kind]
		if !ok {
			continue
		}
		action, ok := workActionByName[s.Action]
		if !ok {
			continue
		}
		res.Resources = append(res.Resources, seededResource{kind: k, x: s.X, y: s.Y, action: action, work: s.Work})
	}
	res.Stations = append(res.Stations, g.spec.Handplaced.Stations...)
	res.Emitters = append(res.Emitters, g.spec.Handplaced.Emitters...)
	for _, l := range g.spec.Handplaced.Loot {
		k, ok := itemKindByName[l.Kind]
		if !ok || l.Count <= 0 {
			continue
		}
		res.Loot = append(res.Loot, LootSeed{Kind: l.Kind, Count: l.Count, X: l.X, Y: l.Y})
		_ = k
	}
}

// seedEmitters 生成手摆效果发射器实体（增益植物/火堆等）。
func seedEmitters(sim *ecs.World, emitters []EmitterSeed) {
	for _, s := range emitters {
		if len(s.Effects) == 0 || s.Radius < 0 {
			continue
		}
		var instances []components.EffectInstance
		for _, ins := range s.Effects {
			if o, ok := effectOrderByName[ins.Order]; ok {
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

// genScatter 程序化撒点：seed 伪随机 + min_dist 冲突检测（确定性尝试）。
func (g *MapGenerator) genScatter(res *MapResult) {
	// 已占位置（手摆资源 + 工作站 + 物资）用于冲突检测
	type pos struct{ x, y int }
	occupied := make([]pos, 0, 32)
	for _, r := range res.Resources {
		occupied = append(occupied, pos{r.x, r.y})
	}
	for _, s := range res.Stations {
		occupied = append(occupied, pos{s.X, s.Y})
	}
	for _, l := range res.Loot {
		occupied = append(occupied, pos{l.X, l.Y})
	}

	for _, rule := range g.spec.Scatter {
		k, ok := itemKindByName[rule.Kind]
		if !ok {
			continue
		}
		action, ok := workActionByName[rule.Action]
		if !ok {
			continue
		}
		if rule.Work <= 0 || rule.MinDist <= 0 {
			continue
		}
		placed := 0
		for attempts := 0; placed < rule.Count && attempts < rule.Count*30; attempts++ {
			x := g.rng.Intn(res.Width)
			y := g.rng.Intn(res.Height)
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
			res.Resources = append(res.Resources, seededResource{kind: k, x: x, y: y, action: action, work: rule.Work})
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

// seedLoot 生成初始可拾取物资实体（Loot）。
func seedLoot(sim *ecs.World, loots []LootSeed) {
	for _, l := range loots {
		k, ok := itemKindByName[l.Kind]
		if !ok || l.Count <= 0 {
			continue
		}
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: l.X, Y: l.Y})
		ecs.Add(sim, e, components.Loot{Items: []components.ItemStack{{Kind: k, Count: l.Count}}})
	}
}

// toProto 把生成结果编码成端上契约（MapConfig）。
func (r *MapResult) toProto() *game.MapConfig {
	return &game.MapConfig{
		Width:         int32(r.Width),
		Height:        int32(r.Height),
		CornerHeights: r.CornerHeights,
		CornerTypes:   r.CornerTypes,
		SpawnX:        int32(r.SpawnX),
		SpawnY:        int32(r.SpawnY),
	}
}
