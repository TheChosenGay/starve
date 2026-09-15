package systems

import (
	"math"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// 关键契约：**本地预测（WorldSnapshot + MoveSolver）与服务端跑出同样的结果**。
//
// 这是"TUI/客户端预测不会与服务端分叉"的根本保证——不是把公式抄一遍，
// 而是真的调用同一个求解器。如果哪天有人把两边拆开实现，这个测试会红。
func TestSnapshotSolverMatchesServer(t *testing.T) {
	// ── 服务端形态：真实 ECS 世界 + 索引 ──
	server := ecs.NewWorld()
	RegisterAll(server, Config{})
	idx := collision.NewIndex()
	server.AddResource(idx)

	// 一棵树（静态，格心圆）+ 一个玩家（动态胶囊）
	tree := server.CreateEntity()
	ecs.Add(server, tree, components.Position{X: 5, Y: 4})
	ecs.Add(server, tree, components.Collide{Shape: components.CollideShapeCircle, Radius: 0.18})
	idx.Set(tree, 5.5, 4.5, 0.18)

	player := server.CreateEntity()
	pp := components.Position{X: 4, Y: 4}
	pmv := components.Moveable{Speed: 10, DirX: 1, SubX: 0.5, SubY: 0.5}
	pcol := components.Collide{Shape: components.CollideShapeCapsule, Radius: 0.305, BodyHeight: 1.5}
	ecs.Add(server, player, pp)
	ecs.Add(server, player, pmv)
	ecs.Add(server, player, pcol)
	idx.SetDynamic(player, 4.5, 4.5, 0.305, 0, 0, 0)

	// ── 本地形态：WorldSnapshot 镜像同样的内容 ──
	snap := NewWorldSnapshot()
	snap.AddStatic(tree, components.Collide{Shape: components.CollideShapeCircle, Radius: 0.18},
		components.Position{X: 5, Y: 4})
	snap.AddDynamic(player, pcol, pp, pmv)

	// 同参数求解
	in := MoveInput{DesiredX: 0.5, DesiredY: 0, DirX: 1, DirY: 0, Speed: 10, DT: 0.05}
	srvSolver := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 20)
	locSolver := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 20)

	sp := ecs.Get[components.Position](server, player)
	smv := ecs.Get[components.Moveable](server, player)
	scol := ecs.Get[components.Collide](server, player)
	srvRes := srvSolver.Solve(server, player, sp, smv, scol, in)

	lp := snap.pos[player]
	lmv := snap.mv[player]
	lcol := snap.col[player]
	locRes := locSolver.Solve(snap.World, player, &lp, &lmv, &lcol, in)

	if math.Abs(srvRes.FinalX-locRes.FinalX) > 1e-12 || math.Abs(srvRes.FinalY-locRes.FinalY) > 1e-12 {
		t.Fatalf("本地预测与服务端分叉: 服务端 (%.9f,%.9f) vs 本地 (%.9f,%.9f)",
			srvRes.FinalX, srvRes.FinalY, locRes.FinalX, locRes.FinalY)
	}
	// 该场景应被树挡住（x 不能走到 5.5-0.305-0.18 之后）
	limit := 5.5 - 0.305 - 0.18
	if got := 4.5 + srvRes.FinalX; got > limit+1e-6 {
		t.Fatalf("应被树干挡住: 终点 x=%.6f > %.6f", got, limit)
	}
}

// 快照每 tick 同步后，动态体位置要跟着更新（否则邻居信息是旧的）。
func TestSnapshotUpdateDynamicMovesShape(t *testing.T) {
	snap := NewWorldSnapshot()
	col := components.Collide{Shape: components.CollideShapeCapsule, Radius: 0.3}
	snap.AddDynamic(1, col, components.Position{X: 0, Y: 0}, components.Moveable{Speed: 10})
	snap.AddDynamic(2, col, components.Position{X: 5, Y: 0}, components.Moveable{Speed: 10})

	// 初始：2 在 5 格外的邻居范围里
	if ns := snap.Index.Neighbors(0, 0, 2, 1); len(ns) != 0 {
		t.Fatalf("5 格外的实体不该被 2 格半径查到, got %d", len(ns))
	}
	// 挪近
	snap.UpdateDynamic(2, components.Position{X: 1, Y: 0}, components.Moveable{Speed: 10})
	if ns := snap.Index.Neighbors(0, 0, 2, 1); len(ns) != 1 {
		t.Fatalf("挪近后应查到 1 个邻居, got %d", len(ns))
	}
}

// 多 tick 连续预测必须稳定（不抖动、不漂移）。
func TestSnapshotRepeatedPredictionStable(t *testing.T) {
	snap := NewWorldSnapshot()
	col := components.Collide{Shape: components.CollideShapeCapsule, Radius: 0.305}
	mv := components.Moveable{Speed: 10, DirX: 1}
	p := components.Position{X: 2, Y: 4}
	snap.AddDynamic(1, col, p, mv)

	solver := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 20)
	x := 2.0
	prev := 0.0
	for i := 0; i < 10; i++ {
		p := snap.pos[1]
		m := snap.mv[1]
		c := snap.col[1]
		res := solver.Solve(snap.World, 1, &p, &m, &c,
			MoveInput{DesiredX: 0.5, DesiredY: 0, DirX: 1, Speed: 10, DT: 0.05})
		// 每 tick 应稳定前进 ~0.5（无阻挡）
		if math.Abs(res.FinalX-0.5) > 0.01 {
			t.Fatalf("空旷处每 tick 应走 ~0.5，第 %d tick 走了 %.4f", i, res.FinalX)
		}
		if prev != 0 && math.Abs(res.FinalX-prev) > 1e-9 {
			t.Fatalf("各 tick 位移应一致（无抖动）: %.9f vs %.9f", res.FinalX, prev)
		}
		prev = res.FinalX
		x += res.FinalX
		snap.UpdateDynamic(1, components.Position{X: int(x), Y: 4},
			components.Moveable{Speed: 10, DirX: 1, SubX: x - math.Floor(x), SubY: 0.5, VelX: res.VelX})
	}
	_ = time.Second
}
