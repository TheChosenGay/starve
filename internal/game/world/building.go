package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// CanPlaceBuilding 查询建筑能否放到 (x,y)（左上角锚点）：占格全部越界/不可走则否。
// 供放置前校验与客户端幽灵预览查询。
func CanPlaceBuilding(wg *worldmap.WalkGrid, x, y, w, h int) bool {
	if wg == nil || w <= 0 || h <= 0 {
		return false
	}
	return wg.AllWalkable(x, y, w, h)
}

// PlaceBuilding 放置建筑：校验占格 → 补 Position → 批量 SetBlocked → placed=true，
// 并按类型挂行为组件（火堆 → HeatSource）。返回是否成功。
func PlaceBuilding(sim *ecs.World, e ecs.Entity, x, y int) bool {
	if !ecs.Has[components.Building](sim, e) {
		return false
	}
	b := ecs.Get[components.Building](sim, e)
	if b.Placed {
		return false
	}
	w, h := buildingWH(b)
	wg, ok := ecs.TryResource[worldmap.WalkGrid](sim)
	if !ok || !CanPlaceBuilding(wg, x, y, w, h) {
		return false
	}
	ecs.Add(sim, e, components.Position{X: x, Y: y})
	wg.SetBlockedRect(x, y, w, h, true)
	b.Placed = true
	ecs.MarkDirty[components.Building](sim, e)
	switch b.Kind {
	case components.BuildingCampfire:
		ecs.Add(sim, e, components.HeatSource{Strength: 10, Radius: 3})
	}
	return true
}

// DemolishBuilding 拆除建筑：批量取消阻挡 → 销毁实体。返回是否成功。
func DemolishBuilding(sim *ecs.World, e ecs.Entity) bool {
	if !ecs.Has[components.Building](sim, e) {
		return false
	}
	b := ecs.Get[components.Building](sim, e)
	if b.Placed {
		if wg, ok := ecs.TryResource[worldmap.WalkGrid](sim); ok {
			if p := ecs.Get[components.Position](sim, e); p != nil {
				w, h := buildingWH(b)
				wg.SetBlockedRect(p.X, p.Y, w, h, false)
			}
		}
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
