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

	// 关掉开关 → 组件摘掉（增量快照会下发移除）
	ecs.Resource[components.DebugFlags](wa.sim).Collision = false
	tickWorld(wa)
	for _, e := range []ecs.Entity{tree, wall, player} {
		if ecs.Has[components.DebugShape](wa.sim, e) {
			t.Fatalf("关掉调试开关后实体 %d 不该还挂 DebugShape", e)
		}
	}
}

// 四足生物：DebugShape 是沿朝向铺开的胶囊（长宽刚好包住模型的契约数据）。
func TestDebugCollisionCapsuleFacing(t *testing.T) {
	wa := moveTestWorld()
	ecs.Resource[components.DebugFlags](wa.sim).Collision = true
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 3, Y: 3})
	ecs.Add(wa.sim, e, components.Moveable{
		Speed:          10,
		DirX:           0,
		DirY:           1, // 朝 +Y（世界 Z）走
		BodyRadius:     0.246,
		BodyHeight:     1.296,
		BodyHalfLength: 1.057,
	})
	tickWorld(wa)

	shape := ecs.Get[components.DebugShape](wa.sim, e)
	if shape.Kind != components.DebugShapeCapsule {
		t.Fatalf("生物应是胶囊, got %+v", *shape)
	}
	if math.Abs(shape.Radius-0.246) > 1e-9 || math.Abs(shape.Height-1.296) > 1e-9 {
		t.Fatalf("半径/身高应与模型推导一致, got %+v", *shape)
	}
	// 轴向 = 最近一次移动方向（+Y）：段沿 Z 铺开
	if math.Abs(shape.AX) > 1e-9 || math.Abs(shape.AZ+1.057) > 1e-9 ||
		math.Abs(shape.BX) > 1e-9 || math.Abs(shape.BZ-1.057) > 1e-9 {
		t.Fatalf("胶囊段应沿朝向铺开, got a=(%.3f,%.3f) b=(%.3f,%.3f)", shape.AX, shape.AZ, shape.BX, shape.BZ)
	}
}
