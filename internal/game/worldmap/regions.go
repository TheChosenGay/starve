package worldmap

import (
	"fmt"
	"math"
	"math/rand"

	game "starve/pkg/proto/game"
)

func regionBiomesOf(regions []RegionInstance) []BiomeType {
	out := make([]BiomeType, len(regions))
	for i, region := range regions {
		out[i] = region.Biome
	}
	return out
}

// RegionInstance 一个已放置的区域实例（id 全局唯一，biome 引用类型表）。
type RegionInstance struct {
	ID      string
	Biome   BiomeType
	X, Y    int
	Size    int
	Near    []string
	FarFrom []string
}

// placeRegions 偏好放置区域实例，返回实例表 + 每格区域 id（1-based，行优先）。
// 确定性：独立 rng（seed + attempt），不影响撒点 rng；连通由调用方 BFS 校验。
func (g *MapGenerator) placeRegions(layout *RegionLayoutSpec, biomes map[BiomeType]BiomeSpec, attempt int, res *MapResult) ([]RegionInstance, []byte) {
	w, h := res.Width, res.Height
	rng := rand.New(rand.NewSource(int64(g.seed) + int64(attempt)*7919))

	regions := []RegionInstance{{
		ID:    "spawn",
		Biome: biomeTypeByName[layout.Spawn],
		X:     g.spec.SpawnX,
		Y:     g.spec.SpawnY,
		Size:  10,
	}}
	for _, rule := range layout.Regions {
		bt, ok := biomeTypeByName[rule.Biome]
		if !ok {
			continue
		}
		if _, ok := biomes[bt]; !ok {
			continue
		}
		count := rule.Count
		if count <= 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			regions = append(regions, RegionInstance{
				ID:      fmt.Sprintf("%s_%d", rule.Biome, i+1),
				Biome:   bt,
				Near:    append([]string(nil), rule.Near...),
				FarFrom: append([]string(nil), rule.FarFrom...),
				Size:    rule.Size,
			})
		}
	}
	for _, idx := range regionPlacementOrder(regions) {
		placeRegion(&regions[idx], regions, rng, w, h, g.spec.Terrain.WaterLevel, res)
	}

	ids := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			best, bd := 0, math.MaxInt
			for i, r := range regions {
				dx, dy := x-r.X, y-r.Y
				d := dx*dx + dy*dy
				if d < bd {
					bd, best = d, i
				}
			}
			ids[y*w+x] = byte(best + 1)
		}
	}
	return regions, ids
}

// regionPlacementOrder 按"到 spawn 的依赖深度"排序：near 依赖先放，无约束最后兜底。
func regionPlacementOrder(regions []RegionInstance) []int {
	placed := map[string]bool{"spawn": true}
	var order []int
	for len(order) < len(regions)-1 {
		progress := false
		for i := 1; i < len(regions); i++ {
			if placed[regions[i].ID] {
				continue
			}
			if hasPlacedNear(regions[i], regions, placed) {
				order = append(order, i)
				placed[regions[i].ID] = true
				progress = true
			}
		}
		if !progress {
			for i := 1; i < len(regions); i++ {
				if !placed[regions[i].ID] {
					order = append(order, i)
					placed[regions[i].ID] = true
				}
			}
			break
		}
	}
	return order
}

func hasPlacedNear(r RegionInstance, all []RegionInstance, placed map[string]bool) bool {
	if len(r.Near) == 0 {
		return placed["spawn"] // 无约束默认贴出生区域，保证主干连通
	}
	for _, n := range r.Near {
		if n == "spawn" && placed["spawn"] {
			return true
		}
		nt, ok := biomeTypeByName[n]
		if !ok {
			continue
		}
		for _, other := range all {
			if other.Biome == nt && placed[other.ID] {
				return true
			}
		}
	}
	return false
}

// placeRegion 绕已放置邻居随机方向放置，取满足 near/far_from + 陆地占比评分最优的候选。
func placeRegion(r *RegionInstance, all []RegionInstance, rng *rand.Rand, w, h, waterLv int, res *MapResult) {
	if r.Size <= 0 {
		r.Size = 12
	}
	r.Size += rng.Intn(5) - 2 // ±2 抖动
	if r.Size < 6 {
		r.Size = 6
	}

	bestX, bestY, bestScore := 0, 0, math.MaxInt
	for cand := 0; cand < 24; cand++ {
		nx, ny := pickNeighbor(*r, all)
		ang := rng.Float64() * 2 * math.Pi
		target := float64(r.Size+regionSize(nx, ny, all)) / 2
		dist := target * (0.9 + rng.Float64()*0.7)
		cx := nx + int(math.Round(math.Cos(ang)*dist))
		cy := ny + int(math.Round(math.Sin(ang)*dist))
		cx = clampInt(cx, 1, w-1)
		cy = clampInt(cy, 1, h-1)
		land := regionLandFraction(cx, cy, r.Size, waterLv, res)
		s := scoreRegion(cx, cy, *r, all) + int((1-land)*3000)
		if land < 0.35 {
			s += 20000 // 大片水域直接重罚，避免区域中心落湖
		}
		if s < bestScore {
			bestX, bestY, bestScore = cx, cy, s
		}
	}
	r.X, r.Y = bestX, bestY
}

// regionLandFraction 采样候选区域圆盘内的"非水"占比（用全局水面线，确定性）。
func regionLandFraction(cx, cy, size, waterLv int, res *MapResult) float32 {
	samples, land := 0, 0
	for a := 0; a < 16; a++ {
		ang := float64(a) / 16 * 2 * math.Pi
		for _, f := range []float64{0.3, 0.6, 0.95} {
			x := cx + int(math.Round(math.Cos(ang)*float64(size)*f))
			y := cy + int(math.Round(math.Sin(ang)*float64(size)*f))
			if x < 0 || y < 0 || x >= res.Width || y >= res.Height {
				continue
			}
			samples++
			if int(res.CornerHeights[y*(res.Width+1)+x]) > waterLv {
				land++
			}
		}
	}
	if samples == 0 {
		return 0
	}
	return float32(land) / float32(samples)
}

// pickNeighbor 从已放置集合里挑一个 near 匹配的邻居（无 near 用出生区域）。
func pickNeighbor(r RegionInstance, all []RegionInstance) (int, int) {
	var candidates []RegionInstance
	if len(r.Near) == 0 {
		candidates = []RegionInstance{all[0]}
	} else {
		for _, n := range r.Near {
			nt, ok := biomeTypeByName[n]
			if !ok {
				continue
			}
			for _, other := range all {
				if other.ID == "spawn" && n == "spawn" {
					candidates = append(candidates, other)
				}
				if other.ID != "spawn" && other.Biome == nt {
					candidates = append(candidates, other)
				}
			}
		}
	}
	if len(candidates) == 0 {
		candidates = []RegionInstance{all[0]}
	}
	c := candidates[0]
	return c.X, c.Y
}

// scoreRegion 评分：near 距离越接近"相切"越好；far_from 越远越好；越界重罚。
func scoreRegion(x, y int, r RegionInstance, all []RegionInstance) int {
	score := 0
	matchedNear := false
	for _, n := range r.Near {
		nt, ok := biomeTypeByName[n]
		if !ok {
			continue
		}
		for _, other := range all {
			if !(other.ID == "spawn" && n == "spawn") && !(other.ID != "spawn" && other.Biome == nt) {
				continue
			}
			matchedNear = true
			dx, dy := x-other.X, y-other.Y
			d := math.Sqrt(float64(dx*dx + dy*dy))
			want := float64(r.Size+other.Size) / 2
			dev := d - want
			score += int(dev * dev)
		}
	}
	if len(r.Near) == 0 {
		matchedNear = true
		dx, dy := x-all[0].X, y-all[0].Y
		d := math.Sqrt(float64(dx*dx + dy*dy))
		dev := d - float64(r.Size+all[0].Size)/2
		score += int(dev * dev)
	}
	for _, f := range r.FarFrom {
		ft, ok := biomeTypeByName[f]
		if !ok {
			continue
		}
		for _, other := range all {
			if !(other.ID == "spawn" && f == "spawn") && !(other.ID != "spawn" && other.Biome == ft) {
				continue
			}
			dx, dy := x-other.X, y-other.Y
			d := math.Sqrt(float64(dx*dx + dy*dy))
			if d < float64(r.Size+other.Size) {
				score += 10000
			}
		}
	}
	if !matchedNear {
		score += 100000 // 没匹配到任何 near（理论上 placement order 已保证）
	}
	return score
}

func regionSize(x, y int, all []RegionInstance) int {
	for _, r := range all {
		if r.X == x && r.Y == y {
			return r.Size
		}
	}
	return 12
}

// regionWeatherOf 从 biome 表取每个区域实例的天气基值（索引 = 区域实例 id）。
func regionWeatherOf(regions []RegionInstance, biomes map[BiomeType]BiomeSpec) []WeatherBias {
	out := make([]WeatherBias, len(regions))
	for i, r := range regions {
		if b, ok := biomes[r.Biome]; ok {
			out[i] = b.Weather
		}
	}
	return out
}

// ValidateRegionConnectivity 从出生点 BFS 可走格子（非水），要求每个区域都可达。
func ValidateRegionConnectivity(ids, tileTypes []byte, w, h int, spawnX, spawnY int) bool {
	if w <= 0 || h <= 0 || len(ids) != w*h || len(tileTypes) != w*h {
		return false
	}
	if spawnX < 0 || spawnY < 0 || spawnX >= w || spawnY >= h {
		return false
	}
	start := spawnY*w + spawnX
	if tileTypes[start] == byte(game.TerrainType_TERRAIN_TYPE_WATER) {
		return false
	}
	seen := make([]bool, w*h)
	reached := map[byte]bool{ids[start]: true}
	queue := []int{start}
	seen[start] = true
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		x, y := i%w, i/w
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := x+d[0], y+d[1]
			if nx < 0 || ny < 0 || nx >= w || ny >= h {
				continue
			}
			j := ny*w + nx
			if seen[j] || tileTypes[j] == byte(game.TerrainType_TERRAIN_TYPE_WATER) {
				continue
			}
			seen[j] = true
			reached[ids[j]] = true
			queue = append(queue, j)
		}
	}
	for _, id := range ids {
		if id != 0 && !reached[id] {
			return false
		}
	}
	return true
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
