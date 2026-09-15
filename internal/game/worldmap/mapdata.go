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
	Occupied       []uint16      // W×H 行优先，静态占位代价层（0=空；>0=穿过该格的额外寻路代价）
	// CreatureOccupied：W×H 行优先，动物占位层（0=无，1=有动物）。
	// 与 Occupied 分开是为了"各清各的"：动物会移动、会与彼此或与树同格，
	// 共用一层会被覆盖写互相清零。放置校验用 BlockedForPlacement 查两层。
	CreatureOccupied []byte

	// reachIDs/reachBuilt：地形连通分量的惰性索引（W×H 行优先；0=不可走，>0=分量 id）。
	// 只由 Reachable/ensureReach 按需建立，不参与存档与协议（服务端内部的派生数据）。
	//
	// 为什么可以长期缓存：**占位物增删不影响连通性**（占位格仍然可走，见 Walkable），
	// 所以树干被砍、建筑放置/拆除都不需要重建；只有"换地图/读档"会——attachMap 调
	// InvalidateReachability。地形（CornerTypes）按契约在 MapData 建好之后是静态的，
	// 真要改它必须显式失效，否则这里的快查会失真。
	reachIDs   []int32
	reachBuilt bool
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

// Reachable 两端是否在同一地形连通分量（四邻接，只看地形可走性；O(1)，首次调用建索引）。
//
// 存在的理由：A* 在"目标不可达"时只能把整个连通分量展开完才敢返回空——默认 128×128
// 地图上是毫秒级，而"目标不可达 → 下一 tick 再来一次"的调用方（`AISystem.chase`）
// 会把它固化成每 tick 的固定开销。分量 id 不同 ⇔ 一定无路，这个判定可以 O(1) 做完。
// 反过来，id 相同 ⇔ 一定存在路径（所有格代价有限），所以快查不会误杀可达目标。
//
// 占位物不影响结果（占位格仍然可走），所以树干被砍/建筑拆除都不需要失效。
func (m *MapData) Reachable(x1, y1, x2, y2 int) bool {
	if m == nil || !m.Walkable(x1, y1) || !m.Walkable(x2, y2) {
		return false
	}
	if x1 == x2 && y1 == y2 {
		return true
	}
	m.ensureReach()
	return m.reachIDs[y1*m.Width+x1] == m.reachIDs[y2*m.Width+x2]
}

// InvalidateReachability 丢弃连通性索引。地形变更（换地图/读档/测试里手改 CornerTypes）
// 之后必须调用，否则 Reachable 会用旧地形的分量 id。
func (m *MapData) InvalidateReachability() {
	if m == nil {
		return
	}
	m.reachBuilt = false
	m.reachIDs = nil
}

// ensureReach 惰性建立连通分量索引：一次 O(W×H) 洪水填充（四邻接，只走地形可走的格），
// 分量 id 从 1 递增，0 留给不可走格。只在第一次 Reachable 时付这一次成本。
func (m *MapData) ensureReach() {
	if m.reachBuilt && len(m.reachIDs) == m.Width*m.Height {
		return
	}
	n := m.Width * m.Height
	ids := make([]int32, n)
	queue := make([]int, 0, n)
	var next int32
	for start := 0; start < n; start++ {
		if ids[start] != 0 || !m.Walkable(start%m.Width, start/m.Width) {
			continue
		}
		next++
		ids[start] = next
		queue = append(queue[:0], start)
		for len(queue) > 0 {
			cur := queue[len(queue)-1]
			queue = queue[:len(queue)-1]
			cx, cy := cur%m.Width, cur/m.Width
			for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
				nx, ny := cx+d[0], cy+d[1]
				if !m.Walkable(nx, ny) {
					continue
				}
				ni := ny*m.Width + nx
				if ids[ni] != 0 {
					continue
				}
				ids[ni] = next
				queue = append(queue, ni)
			}
		}
	}
	m.reachIDs, m.reachBuilt = ids, true
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

// IsOccupied 该格是否已被**静态**占位物（树/岩/建筑/工作站）占据。
func (m *MapData) IsOccupied(x, y int) bool {
	return m.OccupiedCostAt(x, y) > 0
}

// SetCreatureOccupied 写入一格"被动物占据"的标记（动物跨格时调用，O(1)）。
//
// 为什么单独一层而不是复用 Occupied：Occupied 是**覆盖写**（Occupied[i] = cost），
// 而"谁占了这一格"会变——动物每 tick 走、两只动物可能同格、动物与树也可能同格。
// 共用一个数组时，A 离开会把 B（或树）的占位一起清零，是隐蔽的"占位凭空消失"bug。
// 分层的代价只是每格多一个 byte，换来"各清各的"，不需要引用计数。
func (m *MapData) SetCreatureOccupied(x, y int, occupied bool) {
	if m == nil || x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return
	}
	m.ensureCreatureOccupied()
	if occupied {
		m.CreatureOccupied[y*m.Width+x] = 1
		return
	}
	m.CreatureOccupied[y*m.Width+x] = 0
}

// IsCreatureOccupied 该格是否被动物占据。
func (m *MapData) IsCreatureOccupied(x, y int) bool {
	if m == nil || len(m.CreatureOccupied) != m.Width*m.Height {
		return false
	}
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return false
	}
	return m.CreatureOccupied[y*m.Width+x] != 0
}

// BlockedForPlacement 该格能否放东西：静态占位与动物占位都算冲突。
// 动物算占格（需求），所以放置校验统一走这个。
func (m *MapData) BlockedForPlacement(x, y int) bool {
	return m.IsOccupied(x, y) || m.IsCreatureOccupied(x, y)
}

// ClearOccupied 清空**静态**占位层（世界构建/读档后按占位物实体重建）。
// 动物层不清：它由每 tick 的同步维护，与静态重建无关。
func (m *MapData) ClearOccupied() {
	if m == nil {
		return
	}
	m.ensureOccupied()
	clear(m.Occupied)
}

// ensureOccupied 惰性分配静态占位层（旧存档/无地图兜底）。
func (m *MapData) ensureOccupied() {
	if len(m.Occupied) != m.Width*m.Height {
		m.Occupied = make([]uint16, m.Width*m.Height)
	}
}

// ensureCreatureOccupied 惰性分配动物占位层。
func (m *MapData) ensureCreatureOccupied() {
	if len(m.CreatureOccupied) != m.Width*m.Height {
		m.CreatureOccupied = make([]byte, m.Width*m.Height)
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

// AllPlaceable 批量判断一个区域能否放东西：地形可走，且占格内没有任何占位物。
//
// 占位包括两类，都算冲突（一个格子只能归一个东西）：
//   - 静态占位物（树/岩/建筑/工作站）；
//   - **动物**（会移动的实体也整格占位，所以不能把东西放在动物身上）。
func (m *MapData) AllPlaceable(x, y, w, h int) bool {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			if !m.Walkable(x+dx, y+dy) || m.BlockedForPlacement(x+dx, y+dy) {
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
				// 只挑既走得进、又没被占住的格：掉落不该落在树干/建筑里，也不该落在动物身上。
				if m.Walkable(x, y) && !m.BlockedForPlacement(x, y) {
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
