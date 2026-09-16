package components

import (
	"math"
	"testing"
)

// 投掷物理与组件契约测试。
//
// 这一层的核心契约：
//   - 最大投掷距离 = 基础距离 × 力量 / 质量（越重越难扔远）
//   - 抛物线参数由**服务端**算出（落点 + 飞行时长 + 重力），客户端只渲染
//   - 水平方向匀速 ⇒ "落到指定位置"天然成立，不需要三维积分

func TestMaxThrowDistanceScalesWithStrengthOverMass(t *testing.T) {
	// 力量 = 质量 ⇒ 基础距离
	if got := MaxThrowDistance(10, 10); got != BaseThrowDistance {
		t.Fatalf("力量=质量时应为基础距离 %d，实际 %d", BaseThrowDistance, got)
	}
	// 力量翻倍 ⇒ 距离翻倍
	if a, b := MaxThrowDistance(10, 10), MaxThrowDistance(20, 10); b != a*2 {
		t.Fatalf("力量翻倍应使距离翻倍：%d -> %d", a, b)
	}
	// 质量翻倍 ⇒ 距离减半（整数除法）
	if a, b := MaxThrowDistance(10, 10), MaxThrowDistance(10, 20); b != a/2 {
		t.Fatalf("质量翻倍应使距离减半：%d -> %d", a, b)
	}
	// 单调性：力量越大越远、质量越大越近
	prev := -1
	for s := 1; s <= 40; s++ {
		d := MaxThrowDistance(s, 10)
		if d < prev {
			t.Fatalf("力量 %d 的距离 %d 不应小于更小力量的 %d", s, d, prev)
		}
		prev = d
	}
}

func TestMaxThrowDistanceRejectsInvalidInput(t *testing.T) {
	cases := []struct{ strength, mass int }{
		{0, 10}, {-1, 10}, {10, 0}, {10, -1}, {0, 0},
	}
	for _, c := range cases {
		if got := MaxThrowDistance(c.strength, c.mass); got != 0 {
			t.Fatalf("力量=%d 质量=%d 应返回 0（不能投掷），实际 %d",
				c.strength, c.mass, got)
		}
	}
}

// 很重的物体可以被"力量不足"拒绝，而不是算出 0 距离后仍允许投掷。
func TestVeryHeavyObjectCannotBeThrown(t *testing.T) {
	// 力量 1、质量 100 ⇒ 1*8/100 = 0 ⇒ 不能扔
	if got := MaxThrowDistance(1, 100); got != 0 {
		t.Fatalf("力量远小于质量时应为 0，实际 %d", got)
	}
}

func TestFlightTicksGrowsWithDistance(t *testing.T) {
	g := DefaultGravity
	prev := 0
	for d := 0.0; d <= 20; d += 2 {
		tk := FlightTicks(d, g)
		if tk < 1 {
			t.Fatalf("距离 %.0f 的飞行时长应 >= 1 tick，实际 %d", d, tk)
		}
		if tk < prev {
			t.Fatalf("距离 %.0f 的飞行时长 %d 不应小于更近距离的 %d", d, tk, prev)
		}
		prev = tk
	}
}

// 飞行时长必须有限且合理：不能因为重力极小算出天文数字。
func TestFlightTicksStaysReasonable(t *testing.T) {
	// 极端小的重力会算出很大的 tick 数——确认它仍在可接受范围，
	// 且不会溢出/负数（这类输入若来自配置错误，不应该让服务器卡死）。
	tk := FlightTicks(8, 0.0001)
	if tk <= 0 {
		t.Fatalf("飞行时长应为正，实际 %d", tk)
	}
	if tk > 100000 {
		t.Fatalf("飞行时长过大（%d），重力配置可能异常", tk)
	}
	// 重力为 0 / 负数时回退到默认值，而不是除零
	for _, g := range []float64{0, -1} {
		if got := FlightTicks(8, g); got < 1 {
			t.Fatalf("非法重力 %.1f 应回退到默认值，实际 %d", g, got)
		}
	}
}

func TestPeakHeightPositiveAndScales(t *testing.T) {
	g := DefaultGravity
	h1 := PeakHeight(10, g)
	h2 := PeakHeight(20, g)
	if h1 <= 0 {
		t.Fatalf("峰值高度应为正，实际 %.3f", h1)
	}
	// h = g*t²/8 ⇒ 时长翻倍，高度变 4 倍
	if math.Abs(h2/h1-4) > 0.001 {
		t.Fatalf("时长翻倍应使峰值高度变 4 倍：%.3f -> %.3f", h1, h2)
	}
	if PeakHeight(0, g) != 0 {
		t.Fatal("0 时长的高度应为 0")
	}
}

// NewThrowArc：落点精确等于给定目标（水平匀速 ⇒ 一定能落到）。
func TestThrowArcLandsExactlyOnTarget(t *testing.T) {
	arc := NewThrowArc(10, 10, 18, 10, DefaultGravity)
	if arc.ToX != 18 || arc.ToY != 10 {
		t.Fatalf("落点应精确等于目标，实际 (%.1f,%.1f)", arc.ToX, arc.ToY)
	}
	if arc.FlightTicks < 1 {
		t.Fatalf("飞行时长应 >= 1，实际 %d", arc.FlightTicks)
	}
	if arc.PeakHeight <= 0 {
		t.Fatalf("峰值高度应为正，实际 %.3f", arc.PeakHeight)
	}
	if arc.Gravity != DefaultGravity {
		t.Fatalf("重力应为默认值，实际 %.3f", arc.Gravity)
	}

	// 逐 tick 插值，最后一 tick 必须**恰好**落在目标上。
	th := Thrown{FromX: arc.FromX, FromY: arc.FromY, ToX: arc.ToX, ToY: arc.ToY,
		FlightTicks: arc.FlightTicks, Gravity: arc.Gravity}
	var lastX, lastY float64
	for i := 0; i < arc.FlightTicks; i++ {
		th.Elapsed = i + 1
		lastX, lastY = th.CurrentXY()
	}
	if math.Abs(lastX-arc.ToX) > 1e-9 || math.Abs(lastY-arc.ToY) > 1e-9 {
		t.Fatalf("最后一 tick 应精确落在目标 (%.2f,%.2f)，实际 (%.3f,%.3f)",
			arc.ToX, arc.ToY, lastX, lastY)
	}
}

// 水平匀速：相邻 tick 的位移应基本一致（不会忽快忽慢）。
func TestThrowHorizontalMotionIsUniform(t *testing.T) {
	th := Thrown{FromX: 0, FromY: 0, ToX: 10, ToY: 0, FlightTicks: 10}
	var prevX float64
	var steps []float64
	for i := 1; i <= 10; i++ {
		th.Elapsed = i
		x, _ := th.CurrentXY()
		steps = append(steps, x-prevX)
		prevX = x
	}
	for i, s := range steps {
		if math.Abs(s-1.0) > 1e-9 {
			t.Fatalf("第 %d 步位移应为 1.0（10 格 / 10 tick），实际 %.4f", i, s)
		}
	}
}

func TestThrowProgressClampedAndSafe(t *testing.T) {
	// FlightTicks 为 0/负：不应除零或越界
	for _, ft := range []int{0, -5} {
		th := Thrown{FromX: 0, FromY: 0, ToX: 5, ToY: 5, FlightTicks: ft}
		if p := th.Progress(); p != 1 {
			t.Fatalf("FlightTicks=%d 时进度应为 1（立即落地），实际 %.2f", ft, p)
		}
	}
	// 超出总时长：进度夹到 1，位置不越过落点
	th := Thrown{FromX: 0, FromY: 0, ToX: 10, ToY: 0, FlightTicks: 5, Elapsed: 99}
	x, _ := th.CurrentXY()
	if math.Abs(x-10) > 1e-9 {
		t.Fatalf("超出时长后位置应停在落点，实际 %.3f", x)
	}
}

// 组件往返存档：飞行中的物体读档后应能继续飞（不丢轨迹）。
func TestThrownCodecRoundTrip(t *testing.T) {
	in := Thrown{
		Thrower: 42, FromX: 1.5, FromY: 2.5,
		ToX: 9.5, ToY: 3.5, FlightTicks: 20, Elapsed: 7, Gravity: DefaultGravity,
	}
	var codec thrownCodec
	raw, err := codec.Encode(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := codec.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Thrower != in.Thrower || out.FlightTicks != in.FlightTicks ||
		out.Elapsed != in.Elapsed {
		t.Fatalf("整数字段未保留：%+v", out)
	}
	// 浮点是 float32 存储，容忍小误差
	for _, c := range []struct {
		name string
		a, b float64
	}{
		{"FromX", out.FromX, in.FromX}, {"FromY", out.FromY, in.FromY},
		{"ToX", out.ToX, in.ToX}, {"ToY", out.ToY, in.ToY},
		{"Gravity", out.Gravity, in.Gravity},
	} {
		if math.Abs(c.a-c.b) > 1e-5 {
			t.Fatalf("%s 未保留：%.6f vs %.6f", c.name, c.a, c.b)
		}
	}
}

func TestThrowableAndThrowerCodecRoundTrip(t *testing.T) {
	var tc throwableCodec
	raw, err := tc.Encode(Throwable{Mass: 7})
	if err != nil {
		t.Fatalf("throwable encode: %v", err)
	}
	tv, err := tc.Decode(raw)
	if err != nil || tv.Mass != 7 {
		t.Fatalf("Throwable 往返失败：%+v err=%v", tv, err)
	}

	var rc throwerCodec
	raw, err = rc.Encode(Thrower{Strength: 13})
	if err != nil {
		t.Fatalf("thrower encode: %v", err)
	}
	rv, err := rc.Decode(raw)
	if err != nil || rv.Strength != 13 {
		t.Fatalf("Thrower 往返失败：%+v err=%v", rv, err)
	}
}
