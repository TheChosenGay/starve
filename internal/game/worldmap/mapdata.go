package worldmap

import (
	"math/rand"

	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// MapData 地图内部数据（世界级 Resource，服务端内部；不下发客户端）。
// 静态地形（高度/类型）在 MapConfig（端上契约）；效果表随存档保存。
// 作为 Resource 挂到 ECS 世界，效果系统（systems.EffectSystem）直接读取，
// 不需要经 WorldActor 中转。
type MapData struct {
	Width, Height  int
	SpawnX, SpawnY int           // 出生点（map.json spawn_x/y），登录创建玩家时使用
	CornerHeights  []byte        // (W+1)×(H+1) 行优先，每角高度（服务端采样用）
	CornerTypes    []byte        // (W+1)×(H+1) 行优先，每角 TerrainType
	TileEffects    []byte        // W×H 行优先，每格 EffectOrder（0=无）
	TileParams     []int8        // W×H 行优先，每格效果参数（有符号，如毒伤/速度百分比；0=默认）
	RegionIDs      []byte        // W×H 行优先，每格区域实例 id（1-based；0=未分配；服务端内部）
	RegionBiomes   []BiomeType   // 索引 0 对应区域实例 id 1
	RegionWeather  []WeatherBias // 区域天气基值（索引 = 区域实例 id；0 位空）
	Occupied       []uint16      // W×H 行优先，占位代价层（0=空；>0=穿过该格的额外寻路代价）
}

func (m *MapData) MapSize() (int, int) {
	if m == nil {
		return 0, 0
	}
	return m.Width, m.Height
}

// Walkable 该格地形是否可走：越界/水（悬崖）不可走。
// 只看地形——**占位物（树/岩/建筑）不影响这里**："能不能进这一格"由地形决定，
// "能不能贴过去"由形状碰撞（collision 包）在移动时决定，"值不值得绕开"由占位代价
// （OccupiedCostAt）在寻路时决定。三层各答一个不同的问题。
func (m *MapData) Walkable(x, y int) bool {
	if m == nil || x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return false
	}
	return terrainWalkable(m, x, y)
}

// SetOccupied 写入一格占位代价（占位物挂载/移除时调用，O(1)）。
// cost ≤ 0 表示清空占用。
func (m *MapData) SetOccupied(x, y, cost int) {
	if m == nil || x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return
	}
	m.ensureOccupied()
	if cost <= 0 {
		m.Occupied[y*m.Width+x] = 0
		return
	}
	m.Occupied[y*m.Width+x] = uint16(cost)
}

// OccupiedCostAt 穿过该格的额外寻路代价（0 = 空，越界/无数据 = 0）。
func (m *MapData) OccupiedCostAt(x, y int) int {
	if m == nil || len(m.Occupied) != m.Width*m.Height {
		return 0
	}
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return 0
	}
	return int(m.Occupied[y*m.Width+x])
}

// IsOccupied 该格是否已被占位物占据（放置冲突校验用）。
func (m *MapData) IsOccupied(x, y int) bool {
	return m.OccupiedCostAt(x, y) > 0
}

// ClearOccupied 清空占位层（世界构建/读档后按占位物实体重建）。
func (m *MapData) ClearOccupied() {
	if m == nil {
		return
	}
	m.ensureOccupied()
	clear(m.Occupied)
}

// ensureOccupied 惰性分配占位层（旧存档/无地图兜底）。
func (m *MapData) ensureOccupied() {
	if len(m.Occupied) != m.Width*m.Height {
		m.Occupied = make([]uint16, m.Width*m.Height)
	}
}

// AllWalkable 批量判断一个区域（左上角 + 宽高）地形是否全部可走。
// 只判地形（水/悬崖）；放置冲突另用 AllPlaceable。
func (m *MapData) AllWalkable(x, y, w, h int) bool {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			if !m.Walkable(x+dx, y+dy) {
				return false
			}
		}
	}
	return true
}

// AllPlaceable 批量判断一个区域能否放东西：地形可走，且占格内没有任何占位物
// （树/岩/建筑/工作站……）。占位即冲突——一个格子里只能有一个占位物。
func (m *MapData) AllPlaceable(x, y, w, h int) bool {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			if !m.Walkable(x+dx, y+dy) || m.IsOccupied(x+dx, y+dy) {
				return false
			}
		}
	}
	return true
}

// terrainWalkable 地形层可走 = 非水（用角地形判断）。
func terrainWalkable(md *MapData, x, y int) bool {
	if len(md.CornerTypes) == (md.Width+1)*(md.Height+1) {
		return game.TerrainType(md.CornerTypes[y*(md.Width+1)+x]) != game.TerrainType_TERRAIN_TYPE_WATER
	}
	return true
}

// TileEffectAt 返回 (x,y) 格的地块效果与参数（越界/无效 = (0,0)）。
func (m *MapData) TileEffectAt(x, y int) (components.EffectOrder, int) {
	if m == nil || len(m.TileEffects) != m.Width*m.Height || len(m.TileParams) != m.Width*m.Height {
		return 0, 0
	}
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return 0, 0
	}
	i := y*m.Width + x
	return components.EffectOrder(m.TileEffects[i]), int(m.TileParams[i])
}

// TileAt 返回 (x,y) 格的角高度与地形类型（取左上角；无数据 = (0, GRASS)）。
// 天气采样/效果派生用：高度影响温度直减率，地形影响湿冷/风等。
func (m *MapData) TileAt(x, y int) (int, game.TerrainType) {
	if m == nil || x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return 0, game.TerrainType_TERRAIN_TYPE_GRASS
	}
	h, typ := 0, game.TerrainType_TERRAIN_TYPE_GRASS
	if len(m.CornerHeights) == (m.Width+1)*(m.Height+1) {
		h = int(m.CornerHeights[y*(m.Width+1)+x])
	}
	if len(m.CornerTypes) == (m.Width+1)*(m.Height+1) {
		typ = game.TerrainType(m.CornerTypes[y*(m.Width+1)+x])
	}
	return h, typ
}

// TileRegionAt 返回 (x,y) 格所属区域实例 id（越界/无区域 = 0）。
func (m *MapData) TileRegionAt(x, y int) int {
	if m == nil || len(m.RegionIDs) != m.Width*m.Height {
		return 0
	}
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return 0
	}
	return int(m.RegionIDs[y*m.Width+x])
}

// BiomeAt 返回格子所属区域实例的 biome；旧档缺少映射时安全返回 UNSPECIFIED。
func (m *MapData) BiomeAt(x, y int) BiomeType {
	id := m.TileRegionAt(x, y)
	if id <= 0 || id > len(m.RegionBiomes) {
		return game.BiomeType_BIOME_TYPE_UNSPECIFIED
	}
	return m.RegionBiomes[id-1]
}

// NearbyWalkable 为每个掉落堆确定性分配半径内的可走格；不足时回退 origin 并允许重叠。
func (m *MapData) NearbyWalkable(origin components.Position, count, radius int, seed uint64) []components.Position {
	if count <= 0 {
		return nil
	}
	candidates := make([]components.Position, 0, (radius*2+1)*(radius*2+1))
	if radius > 0 {
		for dy := -radius; dy <= radius; dy++ {
			for dx := -radius; dx <= radius; dx++ {
				if dx == 0 && dy == 0 {
					continue
				}
				if distance := absInt(dx) + absInt(dy); distance > radius {
					continue
				}
				x, y := origin.X+dx, origin.Y+dy
				// 只挑既走得进、又没被占位物占住的格：掉落不该落在树干/建筑里。
				if m.Walkable(x, y) && !m.IsOccupied(x, y) {
					candidates = append(candidates, components.Position{X: x, Y: y})
				}
			}
		}
	}
	rng := rand.New(rand.NewSource(int64(seed)))
	rng.Shuffle(len(candidates), func(i, j int) { candidates[i], candidates[j] = candidates[j], candidates[i] })
	out := make([]components.Position, count)
	for i := range out {
		if i < len(candidates) {
			out[i] = candidates[i]
		} else {
			out[i] = origin
		}
	}
	return out
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// RegionBiasAt 返回 (x,y) 格所属区域的天气基值（无区域/越界 = 零值）。
func (m *MapData) RegionBiasAt(x, y int) WeatherBias {
	id := m.TileRegionAt(x, y)
	if id <= 0 || id > len(m.RegionWeather) {
		return WeatherBias{}
	}
	return m.RegionWeather[id-1]
}
