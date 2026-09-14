package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// DebugShapeSystem 调试形状下发（order 96，移动之后）：世界开 GATE_DEBUG_COLLISION 时，
// 给每个有简化碰撞体的实体挂 DebugShape 组件，随快照下发；客户端画出来核对
// "碰撞体是不是刚好包住渲染模型"。关掉开关时摘掉组件（增量快照下发移除）。
//
// 形状来源与真实碰撞一致，坐标一律表达在**实体节点局部空间**（节点位置/朝向由客户端
// 按实体渲染规则给出：占位物 = 占格中心，移动体 = 连续位置与朝向）：
//   - 占位物（树/岩/建筑）：Block（radius>0 = 格心圆柱，否则 = 以节点为中心的占格盒）；
//   - 移动体（玩家/生物）：Moveable 的身体胶囊，四足的段沿**模型局部 +Z**（客户端把
//     局部 +Z 对准朝向），服务端不预先旋转。
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
		height := mv.BodyHeight
		if height <= 0 {
			height = 2 * body.Radius
		}
		// 胶囊段表达在**模型局部空间**，且沿局部 +Z（客户端把局部 +Z 对准朝向，
		// 见 Godot 客户端 IsoCamera3D.FacingYaw）——节点旋转由客户端施加，
		// 服务端再自己转一次会被转两遍，方向误差恰好等于朝向角
		// （+Y 正确、对角差 45°、+X 差 90°）。
		//
		// y 只用于让调试形状压在渲染模型身上（真实碰撞只用水平截面 + 固定平面
		// moverPlaneY），所以取身体竖直中心：直立取 [r, 高度-r]，
		// 这样客户端按"段长 + 2r"画出来的总高正好等于身高；四足取身高一半。
		var ay, by float64
		var az, bz float64
		if half := mv.BodyHalfLength; half > 0 {
			ay, by = height/2, height/2
			az, bz = -half, half
		} else if r := body.Radius; height > 2*r {
			ay, by = r, height-r
		} else {
			ay, by = height/2, height/2
		}
		writeDebugShape(w, e, components.DebugShape{
			Kind:   components.DebugShapeCapsule,
			Radius: body.Radius,
			AY:     ay,
			AZ:     az,
			BY:     by,
			BZ:     bz,
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
