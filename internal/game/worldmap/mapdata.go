package worldmap

import (
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
	RegionWeather  []WeatherBias // 区域天气基值（索引 = 区域实例 id；0 位空）
	Blocked        []byte        // W×H 行优先，动态阻挡层（0=无阻挡 1=阻挡；建筑等 Block 实体写入）
}

// Walkable 该格是否可走：越界/水/动态阻挡都不可走。
// 地形层由 CornerTypes 即时推导（非水），动态层读 Blocked——一块地图、单一数据源。
func (m *MapData) Walkable(x, y int) bool {
	if m == nil || x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return false
	}
	if len(m.Blocked) == m.Width*m.Height && m.Blocked[y*m.Width+x] != 0 {
		return false
	}
	return terrainWalkable(m, x, y)
}

// SetBlocked 增量更新一格可走性（Block 实体挂载/卸载时调用，O(1)）。
func (m *MapData) SetBlocked(x, y int, blocked bool) {
	if m == nil || x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return
	}
	m.ensureBlocked()
	if blocked {
		m.Blocked[y*m.Width+x] = 1
	} else {
		m.Blocked[y*m.Width+x] = 0
	}
}

// SetBlockedRect 批量设置一个区域（左上角 + 宽高）的可走性（建筑占格/拆除用）。
func (m *MapData) SetBlockedRect(x, y, w, h int, blocked bool) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			m.SetBlocked(x+dx, y+dy, blocked)
		}
	}
}

// AllWalkable 批量判断一个区域（左上角 + 宽高）是否全部可走（建筑放置校验用）。
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

// ClearBlocked 清空动态阻挡层（世界构建/存档恢复后按 Block 实体重建）。
func (m *MapData) ClearBlocked() {
	if m == nil {
		return
	}
	m.ensureBlocked()
	clear(m.Blocked)
}

// ensureBlocked 惰性分配阻挡层（旧存档/无地图兜底）。
func (m *MapData) ensureBlocked() {
	if len(m.Blocked) != m.Width*m.Height {
		m.Blocked = make([]byte, m.Width*m.Height)
	}
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

// RegionBiasAt 返回 (x,y) 格所属区域的天气基值（无区域/越界 = 零值）。
func (m *MapData) RegionBiasAt(x, y int) WeatherBias {
	id := m.TileRegionAt(x, y)
	if id <= 0 || id > len(m.RegionWeather) {
		return WeatherBias{}
	}
	return m.RegionWeather[id-1]
}
