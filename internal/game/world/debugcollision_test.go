package world

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
)

// 调试开关打开：占位物（树=圆柱 / 建筑=盒）与移动体（身体胶囊）都挂 DebugShape，
// 数值就是服务端实际碰撞用的形状；关掉开关组件被摘掉。
func TestDebugCollisionShapes(t *testing.T) {
	wa := moveTestWorld()
	ecs.Resource[components.DebugFlags](wa.sim).Collision = true // 世界构建后改开关=改进程内 resource

	tree := addTreeBlocker(wa, 5, 4, 0.18) // 格心圆
	wall := addWallBlocker(wa, 2, 6, 2, 1) // 占格盒
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 4, Y: 4})
	tickWorld(wa)

	treeShape := ecs.Get[components.DebugShape](wa.sim, tree)
	if treeShape.Kind != components.DebugShapeCapsule || math.Abs(treeShape.Radius-0.18) > 1e-9 {
		t.Fatalf("树应该是半径 0.18 的圆柱, got %+v", *treeShape)
	}
	wallShape := ecs.Get[components.DebugShape](wa.sim, wall)
	if wallShape.Kind != components.DebugShapeBox || wallShape.Width != 2 || wallShape.Depth != 1 {
		t.Fatalf("墙应该是 2×1 的盒, got %+v", *wallShape)
	}
	playerShape := ecs.Get[components.DebugShape](wa.sim, player)
	if math.Abs(playerShape.Radius-systems.BodyRadius) > 1e-9 ||
		math.Abs(playerShape.Height-systems.BodyHeight) > 1e-9 {
		t.Fatalf("玩家胶囊应等于缺省身体（r=%.3f h=%.3f）, got %+v", systems.BodyRadius, systems.BodyHeight, *playerShape)
	}
	// 直立胶囊的段取 [r, 身高-r]：客户端按"段长 + 2r"画出来的总高正好 = 身高，
	// 且立在地面之上（AY=0 会让整条身体半截埋进地里）。
	if math.Abs(playerShape.AY-systems.BodyRadius) > 1e-9 ||
		math.Abs(playerShape.BY-(systems.BodyHeight-systems.BodyRadius)) > 1e-9 {
		t.Fatalf("玩家胶囊段应在 [r, 身高-r]，got a_y=%.3f b_y=%.3f", playerShape.AY, playerShape.BY)
	}
	if math.Abs(playerShape.AZ) > 1e-9 || math.Abs(playerShape.BZ) > 1e-9 {
		t.Fatalf("直立胶囊不应有水平段, got a_z=%.3f b_z=%.3f", playerShape.AZ, playerShape.BZ)
	}

	// 关掉开关 → 组件摘掉（增量快照会下发移除）
	ecs.Resource[components.DebugFlags](wa.sim).Collision = false
	tickWorld(wa)
	for _, e := range []ecs.Entity{tree, wall, player} {
		if ecs.Has[components.DebugShape](wa.sim, e) {
			t.Fatalf("关掉调试开关后实体 %d 不该还挂 DebugShape", e)
		}
	}
}

// 四足生物：DebugShape 的段沿**模型局部 +Z**，而不是世界朝向。
// 客户端把局部 +Z 对准朝向（实体节点的 Y 旋转），所以服务端预先旋转会被转两遍——
// 方向误差恰好等于朝向角：+Y 看起来是对的，对角差 45°，+X 整整差 90°。
// 这条用 +X 朝向锁住它（+Y 朝向对 bug 免疫，不能作为判据）。
func TestDebugCollisionCapsuleAxisStaysModelLocal(t *testing.T) {
	const (
		radius = 0.246
		height = 1.296
		half   = 1.057
	)
	wa := moveTestWorld()
	ecs.Resource[components.DebugFlags](wa.sim).Collision = true

	// 朝向 +X（世界 +X）——最容易暴露"被转两遍"的方向
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 3, Y: 3})
	ecs.Add(wa.sim, e, components.Moveable{Speed: 10, DirX: 1, DirY: 0})
	ecs.Add(wa.sim, e, components.Dynamic{})
	ecs.Add(wa.sim, e, components.Collide{
		Shape:      components.CollideShapeCapsule,
		Radius:     radius,
		HalfLength: half,
		BodyHeight: height,
	})
	tickWorld(wa)

	shape := ecs.Get[components.DebugShape](wa.sim, e)
	if shape.Kind != components.DebugShapeCapsule {
		t.Fatalf("生物应是胶囊, got %+v", *shape)
	}
	if math.Abs(shape.Radius-radius) > 1e-9 || math.Abs(shape.Height-height) > 1e-9 {
		t.Fatalf("半径/身高应与模型推导一致, got %+v", *shape)
	}
	// 段沿局部 +Z：朝向为 +X 时也**不能**变成沿 X（那是把朝向预先转进去了）
	if math.Abs(shape.AX) > 1e-9 || math.Abs(shape.BX) > 1e-9 {
		t.Fatalf("段应沿模型局部 +Z（不能预先转到世界朝向）, got a=(%.3f,%.3f,%.3f) b=(%.3f,%.3f,%.3f)",
			shape.AX, shape.AY, shape.AZ, shape.BX, shape.BY, shape.BZ)
	}
	if math.Abs(shape.AZ+half) > 1e-9 || math.Abs(shape.BZ-half) > 1e-9 {
		t.Fatalf("段长应为 ±半长, got a_z=%.3f b_z=%.3f", shape.AZ, shape.BZ)
	}
	// 竖直位置取身体中心：画出来正好压在渲染模型身上（不是躺在脚底）
	if math.Abs(shape.AY-height/2) > 1e-9 || math.Abs(shape.BY-height/2) > 1e-9 {
		t.Fatalf("四足胶囊应取身高中心 y=%.3f, got a_y=%.3f b_y=%.3f", height/2, shape.AY, shape.BY)
	}

	// 朝向换成 +Y（对旧 bug 免疫的方向）：段必须**完全不变**——证明不再随朝向漂移
	ecs.Set(wa.sim, e, components.Moveable{
		Speed: 10, DirX: 0, DirY: 1,
	})
	tickWorld(wa)
	turned := ecs.Get[components.DebugShape](wa.sim, e)
	if *turned != *shape {
		t.Fatalf("形状不应随朝向变化（客户端负责旋转）:\n +X: %+v\n +Y: %+v", *shape, *turned)
	}
}
