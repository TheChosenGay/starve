package components

// MapData 地图内部数据（世界级 Resource，服务端内部；不下发客户端）。
// 静态地形（高度/类型）在 MapConfig（端上契约）；效果表随存档保存。
// 作为 Resource 挂到 ECS 世界，效果系统（systems.EffectSystem）直接读取，
// 不需要经 WorldActor 中转。
type MapData struct {
	Width, Height int
	TileEffects   []byte // W×H 行优先，每格 EffectOrder（0=无）
	TileParams    []int8 // W×H 行优先，每格效果参数（有符号，如毒伤/速度百分比；0=默认）
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
