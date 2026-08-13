package components

import game "starve/pkg/proto/game"

// MapData 地图内部数据（世界级 Resource，服务端内部；不下发客户端）。
// 静态地形（高度/类型）在 MapConfig（端上契约）；效果表随存档保存。
// 作为 Resource 挂到 ECS 世界，效果系统（systems.EffectSystem）直接读取，
// 不需要经 WorldActor 中转。
type MapData struct {
	Width, Height  int
	SpawnX, SpawnY int    // 出生点（map.json spawn_x/y），登录创建玩家时使用
	CornerHeights  []byte // (W+1)×(H+1) 行优先，每角高度（服务端采样用）
	CornerTypes    []byte // (W+1)×(H+1) 行优先，每角 TerrainType
	TileEffects    []byte // W×H 行优先，每格 EffectOrder（0=无）
	TileParams     []int8 // W×H 行优先，每格效果参数（有符号，如毒伤/速度百分比；0=默认）
}

// TileEffectAt 返回 (x,y) 格的地块效果与参数（越界/无效 = (0,0)）。
func (m *MapData) TileEffectAt(x, y int) (EffectOrder, int) {
	if m == nil || len(m.TileEffects) != m.Width*m.Height || len(m.TileParams) != m.Width*m.Height {
		return 0, 0
	}
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return 0, 0
	}
	i := y*m.Width + x
	return EffectOrder(m.TileEffects[i]), int(m.TileParams[i])
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
