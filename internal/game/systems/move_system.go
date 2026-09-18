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
	// 邻居表的 tick 边界工作：
	//   - OrcaAOI：把动态体增量登记进网格（跨格才动桶）；
	//   - 降频方案：判一次是否重查（必须每 tick 一次而不是每实体一次）。
	solver.SyncOrcaAOI(w)
	solver.RefreshNeighborCache()

	// 追步：控制层本 tick 可能消费了**多条** Move 操作（客户端每 tick 采样一条，
	// 网络抖动会把几条挤进同一个服务端 tick）。每条 Move 必须配**一步**移动积分，
	// 所以这里按"子步"跑多轮 —— 每轮用各自那条操作的方向，且所有追赶中的实体一起做两阶段
	// （保持"先全部求解、再统一提交"的顺序无关性）。
	steps := consumedMoveSteps(w)
	zeroIdleOpDrivenVelocity(w, steps)
	rounds := 1
	for _, dirs := range steps {
		if len(dirs) > rounds {
			rounds = len(dirs)
		}
	}
	for round := 0; round < rounds; round++ {
		s.runMovementRound(w, solver, dtSec, steps, round)
	}

	// 提交后刷新动态体：DebugShape / 下一次同步 / 外部查询都读最新值。
	SyncDynamicBodies(w)
}

// zeroIdleOpDrivenVelocity 把"操作驱动、但本 tick 没消费到任何操作"的实体的速度归零。
//
// 为什么要单独做：这类实体被判为"本 tick 不参与移动"（见 ControlQueue.OpDriven），
// 于是 runMovementRound 里那句"停止 ⇒ 清速度"的逻辑根本不会执行到它。速度留在上一 tick 的值，
// 别的客户端就会拿它做外推 —— 明明服务端一步没走，远端却看到他继续滑。
func zeroIdleOpDrivenVelocity(w *ecs.World, steps map[ecs.Entity][]components.MoveDir) {
	q, ok := ecs.TryResource[ControlQueue](w)
	if !ok || len(q.OpDriven) == 0 {
		return
	}
	for actor := range q.OpDriven {
		if len(steps[actor]) > 0 {
			continue // 本 tick 有操作 ⇒ 正常参与移动
		}
		if !ecs.Has[components.Moveable](w, actor) {
			continue
		}
		mv := ecs.Get[components.Moveable](w, actor)
		if mv.VelX == 0 && mv.VelY == 0 {
			continue
		}
		mv.VelX, mv.VelY = 0, 0
		ecs.MarkDirty[components.Moveable](w, actor)
	}
}

// consumedMoveSteps 取本 tick 控制层消费掉的逐步方向（没有控制队列时返回 nil）。
func consumedMoveSteps(w *ecs.World) map[ecs.Entity][]components.MoveDir {
	q, ok := ecs.TryResource[ControlQueue](w)
	if !ok {
		return nil
	}
	return q.Steps
}

// participatesInRound 该实体在本子步里要不要动：
//   - 有逐步方向表的：只跑到自己步数用完为止；
//   - 没有的（AI/生物/本 tick 没有新操作）：只跑常规的第 0 轮。
func participatesInRound(steps map[ecs.Entity][]components.MoveDir, e ecs.Entity, round int) bool {
	if dirs, ok := steps[e]; ok {
		return round < len(dirs)
	}
	return round == 0
}

// dirForRound 本子步的方向：有逐步方向表就用它自己的，否则用常规有效方向（输入/Path 队首）。
func dirForRound(steps map[ecs.Entity][]components.MoveDir, e ecs.Entity, mv *components.Moveable, round int) components.MoveDir {
	if dirs, ok := steps[e]; ok && round < len(dirs) {
		return dirs[round]
	}
	return effectiveDir(mv)
}

// runMovementRound 跑一轮移动（两阶段：先全部求解，再按 id 顺序提交）。
func (s *MoveSystem) runMovementRound(
	w *ecs.World, solver *MoveSolver, dtSec float64,
	steps map[ecs.Entity][]components.MoveDir, round int,
) {
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
		if !participatesInRound(steps, e, round) {
			continue
		}
		mv := ecs.Get[components.Moveable](w, e)
		p := ecs.Get[components.Position](w, e)
		effectSpd := effectiveSpeed(w, e, mv.Speed)
		changed := mv.EffectiveSpeed != effectSpd
		if changed {
			mv.EffectiveSpeed = effectSpd
		}
		dir := dirForRound(steps, e, mv, round)
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
		// SubX/SubY 的**变化量**必须在 ApplyDisplacement 之后才能读到，
		// 它代表"这一 tick 的连续位移"，是客户端插值/外推的唯一依据。
		preSubX, preSubY := it.mv.SubX, it.mv.SubY
		preX, preY := it.p.X, it.p.Y
		prevVelX, prevVelY := it.mv.VelX, it.mv.VelY
		crossed := ApplyDisplacement(w, it.p, it.mv, it.dir, it.res.FinalX, it.res.FinalY)
		subMoved := it.mv.SubX != preSubX || it.mv.SubY != preSubY

		// VelX/VelY 必须反映格子层**真正接受**的位移，而不是求解器的期望位移。
		//
		// 这里曾经先把 res.VelX/res.VelY 写进组件、再交给 ApplyDisplacement，
		// 于是贴墙/贴崖时 stepAxis 把 sub 钳在 0.999 整段拒收（本 tick 零位移），
		// 组件里却仍是满速。两个受害者：
		//   ① 客户端 PositionSmoother：它把 vel==(0,0) 当作"服务端确认停止"来
		//      关闭外推；贴墙仍报满速 → 朝不可走方向外推 → 下个样本残差超过
		//      blendThreshold → 200ms 混合拉回，就是岸边/崖边的橡皮筋。
		//   ② 客户端 SyncOrcaNeighbors 把邻居速度喂进本地 ORCA 预测，会把
		//      "其实被岸挡住"的生物当成 10 格/秒运动体，导致错误避让。
		//
		// 组件与 proto 的契约本就是"实际速度、静止时为 (0,0)"。位移 = 锚点整格差
		// + 子格差（跨格时 p.X/p.Y 也会变，必须一起算），再除以 dt 得到格/秒。
		it.mv.VelX, it.mv.VelY = velocityOf(
			float64(it.p.X-preX)+(it.mv.SubX-preSubX),
			float64(it.p.Y-preY)+(it.mv.SubY-preSubY),
			dtSec,
		)
		mvChanged := it.speedChanged || prevVelX != it.mv.VelX || prevVelY != it.mv.VelY

		if crossed {
			ecs.MarkDirty[components.Position](w, it.e)
		}
		// **连续性契约**：只要连续位置（Position + Sub）变了就要下发，
		// 而不是只在不跨格时不下发。
		//
		// 这里曾经只判 `crossed`，导致"移动中的实体每 2 tick 才下发一次"
		// （10 格/秒 ÷ 20Hz = 0.5 格/tick，即两次 tick 才跨一格）。
		// 客户端渲染 60FPS，却只拿到 10Hz 的有效位置更新 ——
		// 表现为**走动时一卡一跳**（实测 61% 的渲染帧位置完全不变，
		// PositionSmoother 长期只有 1 个样本、走"单样本"分支直接钉住）。
		//
		// 客户端的 PositionSmoother 一直假设"服务端每 tick 广播子格偏移"
		// （见其 delayTicks 注释），但服务端从未实现该契约 —— 这是契约与
		// 实现脱节。sub 随位移连续变化，必须每 tick 下发。
		if subMoved || mvChanged {
			ecs.MarkDirty[components.Moveable](w, it.e)
		}
	}
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
