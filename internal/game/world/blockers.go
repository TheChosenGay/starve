package world

import (
	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// blockerIndex 是占位物的世界侧写入目标（实现 components.BlockerTarget）：
// 每个占位物同时写进两个投影——
//   - collision.World：形状层。移动时扫掠 + 沿接触切面滑动，决定角色能贴到多近；
//   - MapData.Occupied：占位层。放置冲突（一格只归一个占位物）+ 寻路代价（绕开占位格）。
//
// 树/岩（格心圆）与建筑（占格盒）走的是同一条路：一格只归一个占位物，
// 占位不等于不可走——走路由形状碰撞拦，绕不绕由寻路代价决定。
//
// 地图数据每次现取（ecs.TryResource）而不是缓存指针：MapData 在种子阶段还不存在，
// 读档时又是整体覆盖（*attachMap），缓存指针会拿到过期/空的目标。
type blockerIndex struct {
	index *collision.World
	// tiles：实体 → 它占住的格（注销/挪位时清占位层用；形状句柄由引擎自管）。
	tiles map[ecs.Entity][][2]int
}

func newBlockerIndex(index *collision.World) *blockerIndex {
	return &blockerIndex{
		index: index,
		tiles: make(map[ecs.Entity][][2]int),
	}
}

// attachMap 挂载/替换地图数据资源，并按当前实体重建占位层（形状索引 + 占位代价）。
// MapData 是服务端内部资源（不进协议）；世界构建、读档、测试统一走这里，
// 保证"地图换了"与"占位层对齐"是同一个动作。
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
		return cur
	}
	a.sim.AddResource(md)
	rebuildBlockers(a.sim, a.blockers)
	return md
}

// SetBlocker 实现 components.BlockerTarget：注册/更新形状与占位。
// circleRadius > 0 → 格心圆柱（占 1 格）；否则 anchor + width×height 的占格盒。
func (b *blockerIndex) SetBlocker(
	w *ecs.World, e ecs.Entity, anchor components.Position, width, height int, circleRadius float64,
) {
	if b == nil || b.index == nil || e == 0 {
		return
	}
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	md, _ := ecs.TryResource[MapData](w)

	// 先清掉上一次登记的占位（实体换位置/换形状时不会留旧占位）。
	if prev, ok := b.tiles[e]; ok && md != nil {
		for _, t := range prev {
			md.SetOccupied(t[0], t[1], 0)
		}
	}

	var tiles [][2]int
	if circleRadius > 0 {
		b.index.Set(e, float64(anchor.X)+0.5, float64(anchor.Y)+0.5, circleRadius)
		tiles = [][2]int{{anchor.X, anchor.Y}}
		if md != nil {
			md.SetOccupied(anchor.X, anchor.Y, worldmap.OccupiedCostThin)
		}
	} else {
		b.index.SetBox(e, float64(anchor.X), float64(anchor.Y), float64(width), float64(height))
		tiles = make([][2]int, 0, width*height)
		for dy := 0; dy < height; dy++ {
			for dx := 0; dx < width; dx++ {
				x, y := anchor.X+dx, anchor.Y+dy
				tiles = append(tiles, [2]int{x, y})
				if md != nil {
					md.SetOccupied(x, y, worldmap.OccupiedCostFull)
				}
			}
		}
	}
	b.tiles[e] = tiles
}

// ClearBlocker 实现 components.BlockerTarget：注销形状与占位（幂等）。
func (b *blockerIndex) ClearBlocker(w *ecs.World, e ecs.Entity) {
	if b == nil || b.index == nil {
		return
	}
	b.index.Clear(e)
	if tiles, ok := b.tiles[e]; ok {
		if md, ok := ecs.TryResource[MapData](w); ok {
			for _, t := range tiles {
				md.SetOccupied(t[0], t[1], 0)
			}
		}
		delete(b.tiles, e)
	}
}

// Len 返回已注册的形状数量（测试/观测用）。
func (b *blockerIndex) Len() int {
	if b == nil || b.index == nil {
		return 0
	}
	return b.index.Len()
}

// rebuildBlockers 全量重建形状与占位层：清空后按当前所有 Block 实体重写。
// 种子实体在 MapData 之前创建、读档时组件挂载顺序也不保证，所以构建/读档收尾
// 统一对账一次，保证两个投影与实体一致。
func rebuildBlockers(sim *ecs.World, blockers *blockerIndex) {
	if blockers == nil {
		return
	}
	blockers.index.Reset()
	clear(blockers.tiles)
	if md, ok := ecs.TryResource[MapData](sim); ok {
		md.ClearOccupied()
	}
	ecs.Query2[components.Block, components.Position](sim, func(e ecs.Entity, block *components.Block, p *components.Position) {
		blockers.SetBlocker(sim, e, *p, block.Width, block.Height, block.Radius)
	})
}
