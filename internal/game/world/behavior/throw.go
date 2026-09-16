package behavior

import (
	"math"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// ThrowBehavior 投掷：把一件带 Throwable 的物体，从**当前手里位置**抛向**目标落点**。
//
// # 两段动作
//
// 投掷在表现上是两段，服务端按同样的语义处理：
//
//	① 手里阶段（windup）：投掷者做动作，被投物还"在手里"。
//	   这一段**可被打断**——动作没做完，物体就不该飞出去。
//	② 抛出阶段（recovery）：物体已离手，沿抛物线飞行，**不可打断**（箭已离弦）。
//
// 本文件只负责 ①→② 的**校验与启动**（决定"能不能扔、扔到哪"）；
// 飞行本身由 ThrowSystem 每 tick 推进（见 systems/throw_system.go）。
//
// # 校验顺序（严格前置，不满足就不进入执行阶段）
//
//  1. 投掷者活着、有 Thrower（力量 > 0）
//  2. 被投物存在、活着、有 Throwable（质量 > 0）
//  3. 被投物**在投掷者身边**（手里）——防止"隔空扔别人脚下的东西"
//  4. 目标落点与起点的距离 <= MaxThrowDistance(力量, 质量)
//  5. 落点在世界内、且可站立（不扔进墙里/地图外）
//
// 全部通过才会真正"抛出"。
type ThrowBehavior struct{}

// ThrowRequest 是一次投掷意图的**完整输入**。
//
// 按设计，意图必须带三样：被投实体、它当前的位置、目标落点。
// 之所以不用 interactive.Behavior 的 (actor, target) 两参数签名：
// 后者装不下落点。投掷因此走自己的入口 Throw()，而不是 Do()。
type ThrowRequest struct {
	Thrown ecs.Entity // 被投掷的实体
	FromX  float64    // 被投物当前水平位置（客户端上报，服务端校验）
	FromY  float64
	ToX    float64 // 目标落点
	ToY    float64
}

// ThrowResult 是投掷校验/执行的结果（供上层生成 action outcome）。
type ThrowResult struct {
	Success bool
	Reason  string              // 失败原因（用于日志/客户端提示）
	Arc     components.ThrowArc // 成功时的抛物线参数
}

// CanThrow 只做校验，不产生任何副作用。
//
// ControlSystem 在接纳投掷意图前调用它——这就是"判断可投掷后才进入后续流程"。
func (ThrowBehavior) CanThrow(w *ecs.World, actor ecs.Entity, req ThrowRequest) (components.ThrowArc, string) {
	if !w.IsAlive(actor) || ecs.Has[components.Dead](w, actor) || ecs.Has[components.Offline](w, actor) {
		return components.ThrowArc{}, "投掷者不可用"
	}
	// ① 投掷者力量（可能来自手持装备，也可能来自自身）
	_, thrower := interactive.ActorCap[interactive.Thrower](w, actor)
	if thrower == nil || thrower.Strength <= 0 {
		return components.ThrowArc{}, "没有投掷能力"
	}
	// ② 被投物
	e := req.Thrown
	if e == 0 || !w.IsAlive(e) || ecs.Has[components.Dead](w, e) || ecs.Has[components.Offline](w, e) {
		return components.ThrowArc{}, "被投掷物不可用"
	}
	if !ecs.Has[components.Throwable](w, e) {
		return components.ThrowArc{}, "该物体不可投掷"
	}
	mass := ecs.Get[components.Throwable](w, e).Mass
	if mass <= 0 {
		return components.ThrowArc{}, "质量非法"
	}

	// ③ 被投物必须在投掷者身边（"在手里"）。
	//
	// 为什么需要这一条：落点由客户端给，若不限制起点，客户端可以声称
	// "这东西本来就在 100 格外"，从而把任意物品扔到任意位置。
	if !ecs.Has[components.Position](w, actor) || !ecs.Has[components.Position](w, e) {
		return components.ThrowArc{}, "位置未知"
	}
	// 用切比雪夫距离（与网格移动/攻击范围口径一致）：手里 = 相邻格或同格。
	if ap, ep := actPos(w, actor), actPos(w, e); !ap.WithinRange(ep, ThrowHandRange) {
		return components.ThrowArc{}, "被投掷物不在手里"
	}
	// 客户端上报的起点要与权威位置一致（容许一点误差，见 ThrowFromTolerance）。
	if absf(req.FromX-float64(actPos(w, e).X)) > ThrowFromTolerance ||
		absf(req.FromY-float64(actPos(w, e).Y)) > ThrowFromTolerance {
		return components.ThrowArc{}, "起点与权威位置不符"
	}

	// ④ 距离校验：落点不能超出"力量 / 质量"允许的最大距离。
	maxD := components.MaxThrowDistance(thrower.Strength, mass)
	if maxD <= 0 {
		return components.ThrowArc{}, "力量不足"
	}
	from := components.Position{X: int(req.FromX), Y: int(req.FromY)}
	dist := euclid(from, components.Position{X: int(req.ToX), Y: int(req.ToY)})
	if dist > float64(maxD) {
		return components.ThrowArc{}, "超出最大投掷距离"
	}

	// ⑤ 落点必须可站立（不扔进墙里或地图外）。
	if !walkableAt(w, int(req.ToX), int(req.ToY)) {
		return components.ThrowArc{}, "落点不可到达"
	}

	return components.NewThrowArc(req.FromX, req.FromY, req.ToX, req.ToY, components.DefaultGravity), ""
}

// Throw 执行投掷：校验通过后把物体从手里"抛出"（进入飞行阶段）。
//
// 返回值里的 Arc 要下发客户端（渲染抛物线）+ 交给 ThrowSystem 推进飞行。
func (ThrowBehavior) Throw(w *ecs.World, actor ecs.Entity, req ThrowRequest) ThrowResult {
	arc, reason := (ThrowBehavior{}).CanThrow(w, actor, req)
	if reason != "" {
		return ThrowResult{Reason: reason}
	}

	// 落地时间点（服务端 tick）：飞行结束后由 ThrowSystem 结算落点。
	if !ecs.Has[components.Thrown](w, req.Thrown) {
		ecs.Add(w, req.Thrown, components.Thrown{})
	}
	th := ecs.Get[components.Thrown](w, req.Thrown)
	*th = components.Thrown{
		Thrower:     actor,
		FromX:       arc.FromX,
		FromY:       arc.FromY,
		ToX:         arc.ToX,
		ToY:         arc.ToY,
		FlightTicks: arc.FlightTicks,
		Elapsed:     0,
		Gravity:     arc.Gravity,
	}
	ecs.MarkDirty[components.Thrown](w, req.Thrown)
	return ThrowResult{Success: true, Arc: arc}
}

// ── 参数与辅助 ────────────────────────────────────────────

const (
	// ThrowHandRange 是"物体算在手里"的最大切比雪夫距离（格）。
	// 取 2：允许被投物在投掷者的相邻格（手里/脚边），但不允许更远。
	ThrowHandRange = 2
	// ThrowFromTolerance 是客户端上报起点与权威位置的容许偏差（格）。
	// 浮点上报 + 本地表现可能有一点点误差，给 0.75 格余量。
	ThrowFromTolerance = 0.75
)

func actPos(w *ecs.World, e ecs.Entity) components.Position {
	if !ecs.Has[components.Position](w, e) {
		return components.Position{}
	}
	return *ecs.Get[components.Position](w, e)
}

func absf(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// euclid 欧氏距离（格）。
//
// 投掷用欧氏而非切比雪夫：抛物线是圆对称的物理轨迹，
// "能扔多远"应当按直线距离算，而不是方形的格数。
// 注意这与 AOI 感知/仇恨传播的方形口径不同，是有意为之。
func euclid(a, b components.Position) float64 {
	dx := float64(a.X - b.X)
	dy := float64(a.Y - b.Y)
	return math.Hypot(dx, dy)
}

// walkableAt 判断落点是否可站立。没有地图数据时（测试/演示）一律放行。
func walkableAt(w *ecs.World, x, y int) bool {
	if x < 0 || y < 0 {
		return false
	}
	if fn, ok := ecs.TryResourceOf[Walkability](w); ok {
		return fn.Walkable(x, y)
	}
	return true
}

// Walkability 是"某格能不能站人"的资源接口（由 world 包注入）。
//
// 用接口而不是直接依赖 worldmap：behavior 包不应依赖地图实现细节。
type Walkability interface {
	Walkable(x, y int) bool
}
