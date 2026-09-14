package systems

import (
	"math"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// DebugShapeSystem 调试形状下发（order 96，移动之后）：世界开 GATE_DEBUG_COLLISION 时，
// 给每个有简化碰撞体的实体挂 DebugShape 组件，随快照下发；客户端画出来核对
// "碰撞体是不是刚好包住渲染模型"。关掉开关时摘掉组件（增量快照下发移除）。
//
// 形状来源与真实碰撞完全一致：
//   - 占位物（树/岩/建筑）：Block（radius>0 = 格心圆柱，否则 = 占格盒）；
//   - 移动体（玩家/生物）：Moveable 的身体胶囊（BodyRadius/BodyHeight/BodyHalfLength +
//     最近一次移动方向 FacingX/FacingY，四足的段沿朝向铺开）。
//
// 值没变就不写组件，避免每 tick 制造增量。
type DebugShapeSystem struct{}

// Update 实现 ECS 系统接口。
func (s *DebugShapeSystem) Update(w *ecs.World, _ time.Duration) {
	enabled := false
	if flags, ok := ecs.TryResource[components.DebugFlags](w); ok {
		enabled = flags.Collision
	}
	if !enabled {
		var stale []ecs.Entity
		ecs.Query[components.DebugShape](w, func(e ecs.Entity, _ *components.DebugShape) {
			stale = append(stale, e)
		})
		for _, e := range stale {
			ecs.Remove[components.DebugShape](w, e)
		}
		return
	}

	ecs.Query2[components.Block, components.Position](w, func(e ecs.Entity, block *components.Block, _ *components.Position) {
		if block.Radius > 0 {
			writeDebugShape(w, e, components.DebugShape{
				Kind:   components.DebugShapeCapsule,
				Radius: block.Radius,
				BY:     collision.SolidHeight, // 圆柱：段从地面到碰撞体高度
				Height: collision.SolidHeight,
				Source: "Block(圆)",
			})
			return
		}
		width, depth := block.Width, block.Height
		if width <= 0 {
			width = 1
		}
		if depth <= 0 {
			depth = 1
		}
		writeDebugShape(w, e, components.DebugShape{
			Kind:   components.DebugShapeBox,
			Width:  float64(width),
			Depth:  float64(depth),
			Height: collision.SolidHeight,
			Source: "Block(盒)",
		})
	})

	ecs.Query[components.Moveable](w, func(e ecs.Entity, mv *components.Moveable) {
		body := BodyOf(0, 0, mv)
		fx, fz := float64(mv.FacingX), float64(mv.FacingY)
		if fx == 0 && fz == 0 {
			fx = 1 // 朝向未知：+X（胶囊对称，只影响朝向）
		}
		if norm := math.Hypot(fx, fz); norm > 0 {
			fx, fz = fx/norm, fz/norm
		}
		height := mv.BodyHeight
		if height <= 0 {
			height = 2 * body.Radius
		}
		writeDebugShape(w, e, components.DebugShape{
			Kind:   components.DebugShapeCapsule,
			Radius: body.Radius,
			AX:     -fx * mv.BodyHalfLength,
			AZ:     -fz * mv.BodyHalfLength,
			BX:     fx * mv.BodyHalfLength,
			BZ:     fz * mv.BodyHalfLength,
			Height: height,
			Source: "Moveable",
		})
	})
}

// writeDebugShape 写/更新调试形状（值没变不动）。
func writeDebugShape(w *ecs.World, e ecs.Entity, want components.DebugShape) {
	if ecs.Has[components.DebugShape](w, e) {
		cur := ecs.Get[components.DebugShape](w, e)
		if *cur == want {
			return
		}
		*cur = want
		ecs.MarkDirty[components.DebugShape](w, e)
		return
	}
	ecs.Add(w, e, want)
}
