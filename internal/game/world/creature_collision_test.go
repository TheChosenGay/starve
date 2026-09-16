package world

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
)

// addCreature 摆一只带 Moveable 的动物（供"玩家被动物挡住"/"动物占格"测试用）。
// 必须挂 AI：AISystem 会遍历所有带 Creature 的实体并读取 AI 组件。
func addCreature(wa *WorldActor, x, y int, radius float64) ecs.Entity {
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: 30, Max: 30})
	ecs.Add(wa.sim, e, components.Creature{
		Kind:    components.CreatureWolf,
		Threats: map[ecs.Entity]int32{},
		HomeX:   x,
		HomeY:   y,
	})
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle})
	addBehaviorTree(wa, e, false)
	ecs.Add(wa.sim, e, components.Moveable{
		Speed: 0, // 静止：本测试只关心"站着也挡人"
	})
	ecs.Add(wa.sim, e, components.Collide{
		Shape:  components.CollideShapeCapsule,
		Radius: radius,
	})
	return e
}

// 玩家不能穿过动物：正面走向动物时被它的碰撞胶囊挡住，只能沿切线绕开。
//
// 注意断言的是"从未与动物重叠"而不是"永远停住"——动物占一格，玩家可以擦着滑过去，
// 那是正确的滑动语义。真正要防的是"从躯体中间穿过去"。
func TestPlayerBlockedByCreature(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5})
	c := addCreature(wa, 5, 4, 0.3)
	// 先跑一 tick，让动态碰撞层把动物的胶囊登记进去（SyncDynamicBodies 在 MoveSystem 里）。
	tickWorld(wa)

	const playerR = systems.BodyRadius
	const creatureR = 0.3
	minDist := playerR + creatureR // 两者表面相切的最小圆心距

	for i := 0; i < 15; i++ {
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1, DY: 0}})
		tickWorld(wa)
		pp := ecs.Get[components.Position](wa.sim, player)
		pmv := ecs.Get[components.Moveable](wa.sim, player)
		cp := ecs.Get[components.Position](wa.sim, c)
		cmv := ecs.Get[components.Moveable](wa.sim, c)
		dx := (float64(pp.X) + pmv.SubX) - (float64(cp.X) + cmv.SubX)
		dy := (float64(pp.Y) + pmv.SubY) - (float64(cp.Y) + cmv.SubY)
		d := math.Sqrt(dx*dx + dy*dy)
		// 允许一点数值容差（skin 回退量级）
		if d < minDist-1e-3 {
			t.Fatalf("第 %d tick 与动物重叠：圆心距 %.4f < 半径和 %.4f（穿模）", i, d, minDist)
		}
	}
}

// 动物被撞到时会把玩家推开一个切向偏移（证明碰撞真的参与了位移解算，
// 而不是被忽略后"恰好没重叠"）。
func TestPlayerDeflectedByCreature(t *testing.T) {
	wa := moveTestWorld()
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 4, Y: 4})
	ecs.Set(wa.sim, player, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.55})
	addCreature(wa, 5, 4, 0.3)
	tickWorld(wa)

	startY := 4.55
	for i := 0; i < 6; i++ {
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1, DY: 0}})
		tickWorld(wa)
	}
	p := ecs.Get[components.Position](wa.sim, player)
	mv := ecs.Get[components.Moveable](wa.sim, player)
	y := float64(p.Y) + mv.SubY
	if math.Abs(y-startY) < 1e-6 {
		t.Fatalf("擦过动物时应产生切向偏移（滑开），实际 y 没变：%.4f", y)
	}
}

// 动物占格：不能把建筑放在动物站着的格子上。
func TestPlacementRejectedOnCreatureTile(t *testing.T) {
	wa := moveTestWorld()
	addCreature(wa, 3, 3, 0.3)
	tickWorld(wa) // 让占格层同步

	md, _ := ecs.TryResource[MapData](wa.sim)
	if !md.IsCreatureOccupied(3, 3) {
		t.Fatalf("动物所在格应被标记为被动物占据")
	}
	if md.AllPlaceable(3, 3, 1, 1) {
		t.Fatalf("不该允许把建筑放在动物所在的格子上")
	}
	// 旁边空格仍然可放
	if !md.AllPlaceable(4, 3, 1, 1) {
		t.Fatalf("动物旁边的空格应可放置")
	}
}

// 动物走开后，它原来的格子应当恢复可放置（占位要精确清除，不能残留）。
func TestCreatureOccupancyClearsAfterMove(t *testing.T) {
	wa := moveTestWorld()
	c := addCreature(wa, 3, 3, 0.3)
	tickWorld(wa)

	md, _ := ecs.TryResource[MapData](wa.sim)
	if !md.IsCreatureOccupied(3, 3) {
		t.Fatalf("移动前应占 (3,3)")
	}

	// 把动物挪到 (6,6)（直接改位置，模拟它走了）
	ecs.Set(wa.sim, c, components.Position{X: 6, Y: 6})
	tickWorld(wa)

	if md.IsCreatureOccupied(3, 3) {
		t.Fatalf("动物离开后 (3,3) 不该还留着占位")
	}
	if !md.IsCreatureOccupied(6, 6) {
		t.Fatalf("动物新位置 (6,6) 应被占用")
	}
	if !md.AllPlaceable(3, 3, 1, 1) {
		t.Fatalf("动物离开后原格应恢复可放置")
	}
}

// 动物死亡/移除后，它占的格也要释放，否则地图会永久不可放置。
func TestCreatureOccupancyClearsAfterRemoval(t *testing.T) {
	wa := moveTestWorld()
	c := addCreature(wa, 3, 3, 0.3)
	tickWorld(wa)

	md, _ := ecs.TryResource[MapData](wa.sim)
	if !md.IsCreatureOccupied(3, 3) {
		t.Fatalf("移除前应占 (3,3)")
	}

	wa.sim.DestroyEntity(c)
	tickWorld(wa)

	if md.IsCreatureOccupied(3, 3) {
		t.Fatalf("动物被移除后占位应释放")
	}
}

// 动物占位与静态占位互不干扰：两者同格/相邻时，各自清除不影响对方。
func TestCreatureAndStaticOccupancyAreIndependent(t *testing.T) {
	wa := moveTestWorld()
	addTreeBlocker(wa, 3, 3, 0.18) // 静态占位 (3,3)
	c := addCreature(wa, 4, 3, 0.3)
	tickWorld(wa)

	md, _ := ecs.TryResource[MapData](wa.sim)
	if !md.IsOccupied(3, 3) {
		t.Fatalf("树的静态占位应在")
	}
	if !md.IsCreatureOccupied(4, 3) {
		t.Fatalf("动物占位应在")
	}

	// 动物离开：只该清动物层，静态层不动
	ecs.Set(wa.sim, c, components.Position{X: 6, Y: 6})
	tickWorld(wa)

	if !md.IsOccupied(3, 3) {
		t.Fatalf("动物移动不该影响树的静态占位（覆盖写 bug 回归）")
	}
	if md.IsCreatureOccupied(4, 3) {
		t.Fatalf("动物原格应释放")
	}
}
