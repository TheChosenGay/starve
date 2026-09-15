package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
	"starve/internal/game/components/effect"
	"starve/internal/game/worldmap"
)

// MoveSystem 移动推进（order 95）：效果（90）之后、生存（100）之前。
// 连续速度模型：每 tick 按 speed×dt 沿有效方向（Path 队首或输入方向）累积子格偏移，
// 跨格时提交到 Position（整格）。
//
// 速度修正百分比作用于 speed（+100% = 翻倍；≤ -100% = 完全冻结）。
// 几何坡度在步进里再乘投影边长比（与客户端 SlopeSpeed 对齐），不写入 EffectiveSpeed。
//
// MoveSystem 是三阶段移动的执行者：它本身只负责"取意图、调求解器、提交位移"，
// 真正的几何解算在 MoveSolver（见 move_solver.go）——服务端与 TUI 本地预测共用那一份。
type MoveSystem struct {
	// Solver 为 nil 时按缺省参数自建（有 ORCA + 邻居查询半径）。
	Solver *MoveSolver
}

// Update 实现 ECS 系统接口。
//
// 两阶段提交（修"相向而行互相顶住/抖动"）：
//
//	阶段一：为本 tick 所有移动实体**分别**求解位移（只读世界，不改位置）；
//	阶段二：按固定顺序（实体 id）统一提交 sub/Position。
//
// 为什么不能"边算边写"：先被解算的人会把自己的新位置暴露给后解算的人，
// 于是两人撞在同一处、下一 tick 又被"起点重叠推出"弹开，来回振荡。
func (s *MoveSystem) Update(w *ecs.World, dt time.Duration) {
	dtSec := dt.Seconds()
	// 动态体先同步到本 tick 起始位置：让所有人的解算都基于同一份快照。
	SyncDynamicBodies(w)

	solver := s.Solver
	if solver == nil {
		// 邻居半径传 0 = 由 τ 与速度上限自动推导（避免与 τ 脱节）。
		solver = NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 0)
	}
	// 邻居表降频的**边界**：每 tick 判一次是否重查（而不是每个实体判一次，
	// 否则每个实体都会刷新，降频就失效了）。
	solver.RefreshNeighborCache()

	// ── 阶段一：求解（只读，不提交）────────────────────────
	type pending struct {
		e            ecs.Entity
		p            *components.Position
		mv           *components.Moveable
		dir          components.MoveDir
		res          MoveResult
		effectSpd    float64
		speedChanged bool
	}
	plan := make([]pending, 0, 16)
	for _, e := range SortMoveEntities(w) {
		mv := ecs.Get[components.Moveable](w, e)
		p := ecs.Get[components.Position](w, e)
		effectSpd := effectiveSpeed(w, e, mv.Speed)
		changed := mv.EffectiveSpeed != effectSpd
		if changed {
			mv.EffectiveSpeed = effectSpd
		}
		dir := effectiveDir(mv)
		if dir.DX == 0 && dir.DY == 0 {
			// 停止：清掉实际速度（客户端据此回到 idle），但保留 sub。
			if mv.VelX != 0 || mv.VelY != 0 {
				mv.VelX, mv.VelY = 0, 0
				ecs.MarkDirty[components.Moveable](w, e)
			} else if changed {
				ecs.MarkDirty[components.Moveable](w, e)
			}
			continue
		}
		if effectSpd <= 0 {
			continue // 速度效果完全冻结
		}
		var col *components.Collide
		if ecs.Has[components.Collide](w, e) {
			col = ecs.Get[components.Collide](w, e)
			if col.FaceX != dir.DX || col.FaceZ != dir.DY {
				// 胶囊轴向跟随最近一次移动意图（静止时保留）
				col.FaceX, col.FaceZ = dir.DX, dir.DY
			}
		}
		wx := float64(p.X) + mv.SubX
		wy := float64(p.Y) + mv.SubY
		dx, dy := DesiredDisplacement(w, dir, effectSpd, dtSec, wx, wy)
		res := solver.Solve(w, e, p, mv, col, MoveInput{
			DesiredX: dx, DesiredY: dy,
			DirX: dir.DX, DirY: dir.DY,
			Speed: effectSpd, DT: dtSec,
		})
		plan = append(plan, pending{e: e, p: p, mv: mv, dir: dir, res: res, effectSpd: effectSpd, speedChanged: changed})
	}

	// ── 阶段二：按 id 顺序提交 ────────────────────────────
	for _, it := range plan {
		mvChanged := it.speedChanged || it.mv.VelX != it.res.VelX || it.mv.VelY != it.res.VelY
		it.mv.VelX, it.mv.VelY = it.res.VelX, it.res.VelY
		if ApplyDisplacement(w, it.p, it.mv, it.dir, it.res.FinalX, it.res.FinalY) {
			ecs.MarkDirty[components.Position](w, it.e)
		}
		if mvChanged {
			ecs.MarkDirty[components.Moveable](w, it.e)
		}
	}
	// 提交后刷新动态体：DebugShape / 下一次同步 / 外部查询都读最新值。
	SyncDynamicBodies(w)
}

// SyncDynamicBodies 把所有**标记为 Dynamic 且带 Collide** 的实体的碰撞形状
// 同步进 collision.Index。位置取 Position + Sub（连续位置），朝向取 Collide.FaceX/FaceZ。
//
// 判据 = 有没有 Moveable 组件（"会不会自己动"）。这是唯一判据：
// 之前用 Static/Dynamic 两个 tag，但那个语义（位置会不会变）与"推不推得动"
// 混在了一起——船（不自己动但推得动）就无法表达。现在拆成两个正交维度：
//   - 会不会自己动 = 有 Moveable（本函数 + ORCA 邻居表）
//   - 推不推得动   = 有 Pushable
//
// 为什么要单独同步而不是在 MoveBody 里顺手写：动态体是"所有人共享的障碍表"，
// 必须在一轮移动开始前就绪，否则先被解算的人看不到后面的人，结果依赖遍历顺序。
func SyncDynamicBodies(w *ecs.World) {
	idx, ok := ecs.TryResource[collision.Index](w)
	if !ok {
		return
	}
	ecs.Query2[components.Collide, components.Position](w, func(e ecs.Entity, col *components.Collide, p *components.Position) {
		// 判据 = 有没有 Moveable（"会不会自己动"），与索引的动态层一致。
		if !components.CanSelfMove(w, e) {
			return
		}
		wx, wy := float64(p.X), float64(p.Y)
		// Moveable 可有可无（静态动态体没有子格偏移）：先 Has 再取，避免 panic。
		if ecs.Has[components.Moveable](w, e) {
			mv := ecs.Get[components.Moveable](w, e)
			wx += mv.SubX
			wy += mv.SubY
		}
		body := BodyOf(wx, wy, col)
		idx.SetDynamic(e, body.X, body.Z, body.Radius, body.HalfLength, body.FaceX, body.FaceZ)
	})
}

func effectiveSpeed(w *ecs.World, e ecs.Entity, base float64) float64 {
	if base <= 0 {
		base = 10
	}
	mod := effect.SpeedModPercent(w, e)
	if 100+mod <= 0 {
		return 0
	}
	return base * float64(100+mod) / 100
}

// stepAxis 推进一轴：返回 (新锚点格, 新 sub, 是否跨格)。
// 跨格时目标格不可走则停在边界（正方向 0.999 / 负方向 0.001，即边界外侧 ε）。
func stepAxis(pos int, sub float64, dir int, dist float64, canWalk func(int) bool) (int, float64, bool) {
	// 贴墙保持：已在边界且目标格不可走 → 不再累积（否则 sub 会在 0.001↔0.5 间震荡，贴墙抖动）
	if (sub <= 0.002 && dir < 0 && !canWalk(pos-1)) ||
		(sub >= 0.998 && dir > 0 && !canWalk(pos+1)) {
		return pos, sub, false
	}
	next := sub + float64(dir)*dist
	if next >= 0 && next < 1 {
		return pos, next, false // 未跨格：只更新分数偏移
	}
	npos := pos + dir
	if !canWalk(int(npos)) {
		if dir > 0 {
			return pos, 0.999, false
		}
		return pos, 0.001, false
	}
	if next < 0 {
		return npos, next + 1, true // 负方向跨格：借位回到 [0,1)
	}
	return npos, next - 1, true
}

// effectiveDir 当前移动方向：路径优先（自动行走/AI 追击），否则用输入方向。
func effectiveDir(mv *components.Moveable) components.MoveDir {
	if len(mv.Path) > 0 {
		return mv.Path[0]
	}
	return components.MoveDir{DX: mv.DirX, DY: mv.DirY}
}

// popPathStep 跨格完成一步路径后弹出队首（方向一致时）。
func popPathStep(mv *components.Moveable, dir components.MoveDir) {
	if len(mv.Path) > 0 && mv.Path[0] == dir {
		mv.Path = mv.Path[1:]
	}
}

// walkable 目标格可走（无地图 = 可走）。
func walkable(w *ecs.World, x, y int) bool {
	if md, ok := ecs.TryResource[worldmap.MapData](w); ok {
		return md.Walkable(x, y)
	}
	return true
}
