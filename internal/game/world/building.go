package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// PlaceBuilding 放置建筑：校验所有占格（越界/不可走）→ 补 Position → 逐格 SetBlocked →
// placed=true，并按类型挂行为组件（火堆 → HeatSource）。返回是否成功。
// pos 为左上角锚点，size 为边长。
func PlaceBuilding(sim *ecs.World, e ecs.Entity, x, y int) bool {
	if !sim.IsAlive(e) || !ecs.Has[components.Building](sim, e) {
		return false
	}
	b := ecs.Get[components.Building](sim, e)
	if b.Placed {
		return false
	}
	size := b.Size
	if size <= 0 {
		size = 1
	}
	wg, ok := ecs.TryResource[worldmap.WalkGrid](sim)
	if !ok || !buildingPlaceable(wg, x, y, size) {
		return false
	}
	ecs.Add(sim, e, components.Position{X: x, Y: y})
	for dy := 0; dy < size; dy++ {
		for dx := 0; dx < size; dx++ {
			wg.SetBlocked(x+dx, y+dy, true)
		}
	}
	b.Placed = true
	ecs.MarkDirty[components.Building](sim, e)
	// 类型行为组件（参数后续进 buildings.json 配置）
	switch b.Kind {
	case components.BuildingCampfire:
		ecs.Add(sim, e, components.HeatSource{Strength: 10, Radius: 3})
	}
	return true
}

// buildingPlaceable 建筑占格全部可放：越界/不可走（水/已占）拒绝。
func buildingPlaceable(wg *worldmap.WalkGrid, x, y, size int) bool {
	for dy := 0; dy < size; dy++ {
		for dx := 0; dx < size; dx++ {
			if !wg.Walkable(x+dx, y+dy) {
				return false
			}
		}
	}
	return true
}

// DemolishBuilding 拆除建筑：逐格取消阻挡 → 销毁实体。返回是否成功。
func DemolishBuilding(sim *ecs.World, e ecs.Entity) bool {
	if !sim.IsAlive(e) || !ecs.Has[components.Building](sim, e) {
		return false
	}
	b := ecs.Get[components.Building](sim, e)
	if b.Placed {
		if wg, ok := ecs.TryResource[worldmap.WalkGrid](sim); ok {
			if p := ecs.Get[components.Position](sim, e); p != nil {
				size := b.Size
				if size <= 0 {
					size = 1
				}
				for dy := 0; dy < size; dy++ {
					for dx := 0; dx < size; dx++ {
						wg.SetBlocked(p.X+dx, p.Y+dy, false)
					}
				}
			}
		}
	}
	sim.DestroyEntity(e)
	return true
}
