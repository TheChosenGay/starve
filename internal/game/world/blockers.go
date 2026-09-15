package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// blockerIndex 是占位物的世界侧写入目标（实现 components.BlockerTarget）。
//
// 它**只**维护占位层（MapData.Occupied）：放置冲突（一格只归一个占位物）+ 寻路代价。
// 碰撞形状由 collideIndex 负责（见 collide_index.go）——这次重构的核心就是把
// "占位"和"碰撞"两个正交关注点拆开，各自独立演化。
//
// 地图数据每次现取（ecs.TryResource）而不是缓存指针：MapData 在种子阶段还不存在，
// 读档时又是整体覆盖（*attachMap），缓存指针会拿到过期/空的目标。
type blockerIndex struct {
	// tiles：实体 → 它占住的格（注销/挪位时清占位层用）。
	tiles map[ecs.Entity][][2]int
}

func newBlockerIndex() *blockerIndex {
	return &blockerIndex{tiles: make(map[ecs.Entity][][2]int)}
}

// SetBlocker 实现 components.BlockerTarget：只写占位层（不碰碰撞形状）。
func (b *blockerIndex) SetBlocker(
	w *ecs.World, e ecs.Entity, anchor components.Position, width, height int, thin bool,
) {
	if b == nil || e == 0 {
		return
	}
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	md, _ := ecs.TryResource[MapData](w)

	// 先清掉上一次登记的占位（实体换位置/换尺寸时不会留旧占位）。
	if prev, ok := b.tiles[e]; ok && md != nil {
		for _, t := range prev {
			md.SetOccupied(t[0], t[1], 0)
		}
	}

	tiles := make([][2]int, 0, width*height)
	for dy := 0; dy < height; dy++ {
		for dx := 0; dx < width; dx++ {
			x, y := anchor.X+dx, anchor.Y+dy
			tiles = append(tiles, [2]int{x, y})
			if md != nil {
				// thin（树/岩的格心圆）：低代价，A* 倾向绕开但仍可达；
				// 否则（建筑/工作站）：高代价，角色挤不进去。
				cost := worldmap.OccupiedCostFull
				if thin {
					cost = worldmap.OccupiedCostThin
				}
				md.SetOccupied(x, y, cost)
			}
		}
	}
	b.tiles[e] = tiles
}

// ClearBlocker 实现 components.BlockerTarget：注销占位（幂等）。
func (b *blockerIndex) ClearBlocker(w *ecs.World, e ecs.Entity) {
	if b == nil {
		return
	}
	if tiles, ok := b.tiles[e]; ok {
		if md, ok := ecs.TryResource[MapData](w); ok {
			for _, t := range tiles {
				md.SetOccupied(t[0], t[1], 0)
			}
		}
		delete(b.tiles, e)
	}
}

// Len 返回已登记的占位物数量（测试/观测用）。
func (b *blockerIndex) Len() int {
	if b == nil {
		return 0
	}
	return len(b.tiles)
}

// rebuildBlockers 全量重建占位层：清空后按当前所有 Block 实体重写。
// 种子实体在 MapData 之前创建、读档时组件挂载顺序也不保证，所以构建/读档收尾
// 统一对账一次，保证占位层与实体一致。
func rebuildBlockers(sim *ecs.World, blockers *blockerIndex) {
	if blockers == nil {
		return
	}
	clear(blockers.tiles)
	if md, ok := ecs.TryResource[MapData](sim); ok {
		md.ClearOccupied()
	}
	ecs.Query2[components.Block, components.Position](sim, func(e ecs.Entity, block *components.Block, p *components.Position) {
		blockers.SetBlocker(sim, e, *p, block.Width, block.Height, block.Thin)
	})
}

// attachMap 挂载/替换地图数据资源，并按当前实体重建占位层与碰撞形状。
// MapData 是服务端内部资源（不进协议）；世界构建、读档、测试统一走这里，
// 保证"地图换了"与"占位/形状对齐"是同一个动作。
func (a *WorldActor) attachMap(md *MapData) *MapData {
	if md == nil {
		return nil
	}
	// 换地图/读档：地形变了，寻路的连通性快查索引必须重建。
	// （占位物增删不影响它——占位格仍然可走，所以游戏过程中不需要失效。）
	md.InvalidateReachability()
	if cur, ok := ecs.TryResource[MapData](a.sim); ok {
		*cur = *md
		rebuildBlockers(a.sim, a.blockers)
		rebuildCollides(a.sim, a.collides)
		// 地图被整体覆盖，动物占位层也一起没了：清掉跟踪表后按当前实体重建，
		// 否则旧格会残留、新格未登记（放置校验会看到错的占格）。
		a.creatureTiles.Reset(a.sim)
		a.creatureTiles.Sync(a.sim)
		return cur
	}
	a.sim.AddResource(md)
	rebuildBlockers(a.sim, a.blockers)
	rebuildCollides(a.sim, a.collides)
	a.creatureTiles.Reset(a.sim)
	a.creatureTiles.Sync(a.sim)
	return md
}
