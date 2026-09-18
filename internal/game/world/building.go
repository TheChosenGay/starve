package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// CanPlaceBuilding 查询建筑能否放到 (x,y)（左上角锚点）：占格全部可走、且不压在
// 形状碰撞体（树干/岩石）上。供放置前校验与客户端幽灵预览查询。
func CanPlaceBuilding(md *worldmap.MapData, x, y, w, h int) bool {
	if md == nil || w <= 0 || h <= 0 {
		return false
	}
	return md.AllPlaceable(x, y, w, h)
}

// 火堆燃料与热量参数（tick；20Hz 下 20 tick = 1 秒）。
//
// 初始 1200 tick = 60 秒：放下去先烧一分钟，够玩家体验"火会灭"，又不至于
// 放完就得立刻去砍柴。上限 4800 tick = 4 分钟 = 正好一个昼夜（systems 的
// dayLengthTicks）：睡前把柴填满就能睡到天亮，不必半夜起来添柴。
const (
	campfireInitialFuelTicks = 20 * 60
	campfireMaxFuelTicks     = 20 * 240
	campfireHeatStrength     = 10
	campfireHeatRadius       = 3
)

// PlaceBuilding 放置建筑：校验占格 → 挂 Position + Block（Block 的 OnAdd 钩子自动写 MapData 阻挡）→ placed=true，
// 并按类型挂行为组件（火堆 → Fuel + HeatSource）。返回是否成功。
func PlaceBuilding(sim *ecs.World, e ecs.Entity, x, y int) bool {
	if !ecs.Has[components.Building](sim, e) {
		return false
	}
	b := ecs.Get[components.Building](sim, e)
	if b.Placed {
		return false
	}
	w, h := buildingWH(b)
	md, ok := ecs.TryResource[worldmap.MapData](sim)
	if !ok || !CanPlaceBuilding(md, x, y, w, h) {
		return false
	}
	// 顺序敏感：Position 先挂，Block/Collide 的 OnAdd 钩子需要读到它。
	ecs.Add(sim, e, components.Position{X: x, Y: y})
	ecs.Add(sim, e, components.Block{Width: w, Height: h})
	ecs.Add(sim, e, components.Collide{Shape: components.CollideShapeBox, Width: w, Height: h})
	b.Placed = true
	ecs.MarkDirty[components.Building](sim, e)
	switch b.Kind {
	case components.BuildingCampfire:
		// 燃料与热量参数一起挂：熄灭的实现是**移除 HeatSource**，
		// 参数若只写在放置逻辑里，复燃时就没人知道这个火堆原本多热。
		fuel := components.Fuel{
			Cur:          campfireInitialFuelTicks,
			Max:          campfireMaxFuelTicks,
			HeatStrength: campfireHeatStrength,
			HeatRadius:   campfireHeatRadius,
		}
		ecs.Add(sim, e, fuel)
		components.RelightHeatSource(sim, e, &fuel)
	}
	return true
}

// DemolishBuilding 拆除建筑：销毁实体（DestroyEntity 先触发 Block.OnRemove 清除阻挡）。返回是否成功。
func DemolishBuilding(sim *ecs.World, e ecs.Entity) bool {
	if !ecs.Has[components.Building](sim, e) {
		return false
	}
	sim.DestroyEntity(e)
	return true
}

// buildingWH 建筑占格尺寸（缺省 1×1）。
func buildingWH(b *components.Building) (int, int) {
	w, h := b.Width, b.Height
	if w <= 0 {
		w = 1
	}
	if h <= 0 {
		h = 1
	}
	return w, h
}
