package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// CanPlaceBuilding 查询建筑能否放到 (x,y)（左上角锚点）：占格全部越界/不可走则否。
// 供放置前校验与客户端幽灵预览查询。
func CanPlaceBuilding(md *worldmap.MapData, x, y, w, h int) bool {
	if md == nil || w <= 0 || h <= 0 {
		return false
	}
	return md.AllWalkable(x, y, w, h)
}

// PlaceBuilding 放置建筑：校验占格 → 挂 Position + Block（Block 的 OnAdd 钩子自动写 MapData 阻挡）→ placed=true，
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
	md, ok := ecs.TryResource[worldmap.MapData](sim)
	if !ok || !CanPlaceBuilding(md, x, y, w, h) {
		return false
	}
	// 顺序敏感：Position 先挂，Block.OnAdd 钩子需要读到它。
	ecs.Add(sim, e, components.Position{X: x, Y: y})
	ecs.Add(sim, e, components.Block{Width: w, Height: h})
	b.Placed = true
	ecs.MarkDirty[components.Building](sim, e)
	switch b.Kind {
	case components.BuildingCampfire:
		ecs.Add(sim, e, components.HeatSource{Strength: 10, Radius: 3})
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
