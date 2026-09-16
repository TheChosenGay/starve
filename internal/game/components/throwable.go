package components

import (
	"math"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// 投掷相关的组件与物理计算。
//
// # 职责划分
//
//   - **服务端**：校验授权与距离、算**水平**轨迹（落点 + 飞行时长）、在落地时结算。
//   - **客户端**：只负责表现（手里的 windup 动作 + 抛出后的抛物线渲染）。
//
// # 高度为什么不在模拟层
//
// 现有 Position 只有 X/Y，碰撞 / AOI / 移动全部是二维的。给被投物引入真正的
// Z 轴会波及所有这些系统（还要处理"空中是否撞墙""能否被空中拦截"）。
// 而投掷的**结果**（落到哪、多久后落地）在二维里就能完整确定：
//
//	给定水平距离 d 与重力 g，反推飞行时长 t，落点自然命中。
//
// 高度因此只作为**表现参数**下发给客户端（见 ThrowArc），由客户端画抛物线。
// 若将来真需要"空中拦截"，再给 Position 加 Z —— 那时本文件的
// ThrowArc 仍可作为初速度来源。

// Throwable 可被投掷（-able）：能被"投掷"这个动作作用。
//
// 与 Pushable 的区别：Pushable 是"顶着它走、它跟着挪"（接触推动），
// Throwable 是"把它拿起来扔出去"（脱离持有者飞行）。
// 一个实体可以同时具备两者（比如一块石头既能推也能扔）。
type Throwable struct {
	// Mass 质量（单位自定，仅用于比较）。质量越大越难扔远：
	// 最大投掷距离 ∝ 投掷者力量 / 质量。
	//
	// 为什么放在 Throwable 里而不是单独的 Mass 组件：质量只在投掷里被使用，
	// 单独拆一个组件会让"有质量却不能扔"变成一种无意义的状态。
	// 若将来战斗/推动也需要质量，再抽出来不迟。
	Mass int
}

// Usable 实现 interactive.Actived：可投掷物永远可被投掷
// （"能不能扔"由投掷者的力量与距离决定，不由物体自身耗尽）。
func (Throwable) Usable(w *ecs.World, e ecs.Entity) bool { return true }

// Thrower 投掷能力（-er）：投掷者的力量。
//
// 力量决定**最大投掷距离**（见 MaxThrowDistance）。它不是"命中率"，
// 而是"能扔多远"——超出范围的投掷意图会被直接拒绝（不进入执行阶段）。
type Thrower struct {
	// Strength 力量。<= 0 视为不能投掷。
	Strength int
}

// ThrowerActRange 是投掷能力的通用范围上限（格）。
//
// 真实上限由"力量 / 质量"算出（MaxThrowDistance），不是固定值。
// 这里给一个足够大的值，让 interactive.Activer 的通用范围前置校验不误拒；
// 真正的距离校验由 throw 行为自己完成。
const ThrowerActRange = 64

// 说明：Thrower 的 Actived/ActRange（interactive.Activer 接口）**不在这里实现**。
// components 包不能 import interactive（会成环：interactive 已经 import components），
// 所以接口方法放在 interactive 包，与 Attacker 的写法一致。

// BaseThrowDistance 是"力量 = 质量"时的投掷距离（格）。
//
// 距离公式：d = BaseThrowDistance * Strength / Mass
// 取 8：一只普通生物的力气刚好把同质量的物体扔出 8 格（约等于狼的拴绳范围），
// 手感上"够得着但不算远"。
const BaseThrowDistance = 8

// MaxThrowDistance 由投掷者力量与被投物质量算出的最大投掷距离（格）。
//
// 返回 0 表示不能投掷（力量或质量非正）。
//
// 为什么用整数除法：结果要参与**距离校验**，而校验必须确定性
// （同输入同输出，实机/读档/重放一致）。浮点在 Go 与 C# 之间、以及
// 不同平台之间都可能有微小差异，整数除法没有这个问题。
// 客户端渲染抛物线时用的是同一套整数参数，误差可忽略。
func MaxThrowDistance(strength, mass int) int {
	if strength <= 0 || mass <= 0 {
		return 0
	}
	return BaseThrowDistance * strength / mass
}

// ThrowArc 是一条抛物线的**表现参数**（下发给客户端渲染）。
//
// 服务端只算水平轨迹；高度只在客户端用于画弧线。
// 两端用同一组参数 ⇒ 不需要各自积分，也就不会出现"两边算得不一样"。
type ThrowArc struct {
	FromX, FromY float64 // 起点（水平，格）
	ToX, ToY     float64 // 落点（水平，格）
	// FlightTicks 飞行时长（tick）。由水平距离与重力反推，见 FlightTicks。
	FlightTicks int
	// PeakHeight 弧线最高点（格）。仅供客户端画弧，不影响落点。
	PeakHeight float64
	// Gravity 重力（格/tick²），客户端用它与 FlightTicks 一起还原抛物线。
	Gravity float64
}

// DefaultGravity 是投掷默认重力（格/tick²）。
//
// 取值影响手感：g 越大，同样距离需要的飞行时间越短、弧线越平。
// 0.02 格/tick² 下，水平抛 8 格约需 28 tick（1.4 秒）——
// 足够让玩家看清弧线并做出反应（躲开或走位）。
const DefaultGravity = 0.02

// FlightTicks 由水平距离与重力反推飞行时长（tick）。
//
// 推导：把水平方向当作匀速（水平初速度 v = d / t），
// 竖直方向从 0 抛到 0（对称抛物线），落地时间 t 与峰值高度 h 满足
// h = g*t²/8。为了让弧线"看起来合理"（峰值高度随距离增长），
// 这里直接令 t = sqrt(2*d/g)，即把 d 当作下落高度来定时长——
// 好处是 t 随距离单调增长、且不同距离的弧线形状稳定。
//
// 至少返回 1 tick：0 会让投掷在同一 tick 内起落，看不出飞行过程。
func FlightTicks(distance float64, gravity float64) int {
	if gravity <= 0 {
		gravity = DefaultGravity
	}
	if distance <= 0 {
		return 1
	}
	t := math.Sqrt(2 * distance / gravity)
	ticks := int(math.Round(t))
	if ticks < 1 {
		ticks = 1
	}
	return ticks
}

// PeakHeight 计算抛物线的最高点（格），仅供客户端画弧。
//
// 由最小二乘/对称性：初速度 v0 = g*t/2，峰值 h = v0²/(2g) = g*t²/8。
func PeakHeight(flightTicks int, gravity float64) float64 {
	if gravity <= 0 {
		gravity = DefaultGravity
	}
	if flightTicks <= 0 {
		return 0
	}
	t := float64(flightTicks)
	return gravity * t * t / 8
}

// NewThrowArc 组装一条抛物线参数（服务端算好后下发给客户端）。
func NewThrowArc(fromX, fromY, toX, toY float64, gravity float64) ThrowArc {
	if gravity <= 0 {
		gravity = DefaultGravity
	}
	// 水平距离：用欧氏距离（抛物线是圆的对称，不是方形）。
	// 注意这与 AOI 的切比雪夫口径**不同**——投掷是物理轨迹，用欧氏才对；
	// 而"超出最大投掷距离"的校验也用欧氏，两者保持一致。
	d := math.Hypot(toX-fromX, toY-fromY)
	ticks := FlightTicks(d, gravity)
	return ThrowArc{
		FromX: fromX, FromY: fromY,
		ToX: toX, ToY: toY,
		FlightTicks: ticks,
		PeakHeight:  PeakHeight(ticks, gravity),
		Gravity:     gravity,
	}
}

// Thrown 是"正在飞行中"的状态（挂在被投物身上，落地后移除）。
//
// 为什么把它做成组件而不是系统内部状态：飞行跨越多 tick，必须能进存档
// （中途存档再读回来，物体应当在原轨迹上继续飞，而不是凭空落地或消失）。
type Thrown struct {
	Thrower ecs.Entity // 投掷者（落地结算时作为 attacker，用于记仇/传播）
	FromX   float64    // 起点（水平，格）
	FromY   float64
	ToX     float64 // 落点（水平，格）
	ToY     float64
	// FlightTicks 总飞行时长；Elapsed 已飞 tick 数。
	FlightTicks int
	Elapsed     int
	// Gravity 重力（格/tick²），随快照下发给客户端还原抛物线。
	Gravity float64
}

// Progress 返回飞行进度 [0,1]。
func (t Thrown) Progress() float64 {
	if t.FlightTicks <= 0 {
		return 1
	}
	p := float64(t.Elapsed) / float64(t.FlightTicks)
	if p > 1 {
		return 1
	}
	return p
}

// CurrentXY 返回当前水平位置（线性插值）。
//
// 水平是匀速的：高度只影响表现（客户端画弧），不影响水平落点。
// 这样服务端不必做三维积分，"落到指定位置"是天然成立的。
func (t Thrown) CurrentXY() (float64, float64) {
	p := t.Progress()
	return t.FromX + (t.ToX-t.FromX)*p, t.FromY + (t.ToY-t.FromY)*p
}

// ── codec ────────────────────────────────────────────────

type throwableCodec struct{}

func (throwableCodec) Encode(v Throwable) ([]byte, error) {
	return pb.Marshal(&game.Throwable{Mass: int32(v.Mass)})
}

func (throwableCodec) Decode(b []byte) (Throwable, error) {
	var m game.Throwable
	if err := pb.Unmarshal(b, &m); err != nil {
		return Throwable{}, err
	}
	return Throwable{Mass: int(m.Mass)}, nil
}

type thrownCodec struct{}

func (thrownCodec) Encode(v Thrown) ([]byte, error) {
	return pb.Marshal(&game.Thrown{
		Thrower:     uint64(v.Thrower),
		FromX:       float32(v.FromX),
		FromY:       float32(v.FromY),
		ToX:         float32(v.ToX),
		ToY:         float32(v.ToY),
		FlightTicks: int32(v.FlightTicks),
		Elapsed:     int32(v.Elapsed),
		Gravity:     float32(v.Gravity),
	})
}

func (thrownCodec) Decode(b []byte) (Thrown, error) {
	var m game.Thrown
	if err := pb.Unmarshal(b, &m); err != nil {
		return Thrown{}, err
	}
	return Thrown{
		Thrower:     ecs.Entity(m.Thrower),
		FromX:       float64(m.FromX),
		FromY:       float64(m.FromY),
		ToX:         float64(m.ToX),
		ToY:         float64(m.ToY),
		FlightTicks: int(m.FlightTicks),
		Elapsed:     int(m.Elapsed),
		Gravity:     float64(m.Gravity),
	}, nil
}

func RegisterThrown(w *ecs.World) { ecs.RegisterComponent(w, "Thrown", thrownCodec{}) }

type throwerCodec struct{}

func (throwerCodec) Encode(v Thrower) ([]byte, error) {
	return pb.Marshal(&game.Thrower{Strength: int32(v.Strength)})
}

func (throwerCodec) Decode(b []byte) (Thrower, error) {
	var m game.Thrower
	if err := pb.Unmarshal(b, &m); err != nil {
		return Thrower{}, err
	}
	return Thrower{Strength: int(m.Strength)}, nil
}

func RegisterThrowable(w *ecs.World) { ecs.RegisterComponent(w, "Throwable", throwableCodec{}) }
func RegisterThrower(w *ecs.World)   { ecs.RegisterComponent(w, "Thrower", throwerCodec{}) }
