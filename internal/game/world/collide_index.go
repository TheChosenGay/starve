package world

import (
	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// collideIndex 是碰撞体的世界侧写入目标（实现 components.CollideTarget）。
//
// 它**只**维护形状层（collision.Index）：移动时扫掠 + 沿接触切面滑动，
// 决定角色"能贴到多近"。占位（放置冲突/寻路代价）由 blockerIndex 负责。
//
// 静态与动态的区分由实体上的 Static / Dynamic 标记决定（不再靠"有没有 Moveable"推断）：
//   - Static：形状一次注册后不再变（fat AABB 余量 0）；
//   - Dynamic：每 tick 由 SyncDynamicBodies 刷新到最新位置（余量 DynamicMargin）。
type collideIndex struct {
	index *collision.Index
}

func newCollideIndex(index *collision.Index) *collideIndex {
	return &collideIndex{index: index}
}

// SetCollide 实现 components.CollideTarget：把 Collide 组件翻译成索引里的形状。
//
// anchor 是实体的格锚点（Position）。不同形状的摆放规则：
//   - Circle：圆心在**格心**（anchor + 0.5）；
//   - Box：左上角就是 anchor，尺寸按占格数；
//   - Capsule：中心在实体位置（移动体用连续位置，见 SyncDynamicBodies 的覆盖），
//     anchor 只用于静态注册时的初值。
func (c *collideIndex) SetCollide(
	w *ecs.World, e ecs.Entity, anchor components.Position, col components.Collide, dynamic bool,
) {
	if c == nil || c.index == nil || e == 0 {
		return
	}
	if dynamic {
		// 动态体：位置由 SyncDynamicBodies 按连续位置（Position + sub）刷新，
		// 这里只保证"已注册"。注册点用格锚点，下一 tick 就会被纠正。
		c.index.SetDynamic(e,
			float64(anchor.X), float64(anchor.Y),
			col.Radius, col.HalfLength,
			float64(col.FaceX), float64(col.FaceZ))
		return
	}
	switch col.Shape {
	case components.CollideShapeCircle:
		c.index.Set(e, float64(anchor.X)+0.5, float64(anchor.Y)+0.5, col.Radius)
	case components.CollideShapeBox:
		c.index.SetBox(e, float64(anchor.X), float64(anchor.Y),
			float64(maxInt(col.Width, 1)), float64(maxInt(col.Height, 1)))
	case components.CollideShapeCapsule:
		// 静态胶囊（少见）：以格心为中心摆。
		c.index.SetDynamic(e,
			float64(anchor.X)+0.5, float64(anchor.Y)+0.5,
			col.Radius, col.HalfLength,
			float64(col.FaceX), float64(col.FaceZ))
	default:
		// 形状未指定：当作没有碰撞体，避免留下旧形状。
		c.index.Clear(e)
	}
}

// ClearCollide 实现 components.CollideTarget：注销形状（幂等）。
// 静态与动态两侧都清，避免实体切换类别时残留。
func (c *collideIndex) ClearCollide(w *ecs.World, e ecs.Entity) {
	if c == nil || c.index == nil {
		return
	}
	c.index.Clear(e)
	c.index.ClearDynamic(e)
}

// Len 返回已注册的碰撞形状数量（测试/观测用）。
func (c *collideIndex) Len() int {
	if c == nil || c.index == nil {
		return 0
	}
	return c.index.TotalLen()
}

// rebuildCollides 全量重建**静态**碰撞形状：清空静态侧后按所有 Collide + Static 实体重写。
// 动态实体由 SyncDynamicBodies 每 tick 维护，不在这里重建（否则读档会清掉动物形状）。
func rebuildCollides(sim *ecs.World, collides *collideIndex) {
	if collides == nil {
		return
	}
	collides.index.Reset()
	ecs.Query2[components.Collide, components.Position](sim, func(e ecs.Entity, col *components.Collide, p *components.Position) {
		if components.CanSelfMove(sim, e) {
			return // 会自己动的实体由 SyncDynamicBodies 每 tick 维护，不走静态重建
		}
		collides.SetCollide(sim, e, *p, *col, false)
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
