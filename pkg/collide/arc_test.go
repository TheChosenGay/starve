package collide

import (
	"math"
	"testing"
)

// TestArcBoundsMatchesSampling：用稠密采样当真值验证闭式解。
// 闭式盒必须包住所有采样点（不漏），且不比采样盒大出采样误差（不过松）。
func TestArcBoundsMatchesSampling(t *testing.T) {
	cases := []struct {
		p, pivot, axis Vec3
		from, to       float64
	}{
		{Vec3{X: 2}, Vec3{}, Vec3{Y: 1}, 0, math.Pi / 2},
		{Vec3{X: 2, Y: 1, Z: 0.5}, Vec3{X: 0.3, Y: 1, Z: -0.2}, Vec3{Y: 1}, -1.2, 2.4},
		{Vec3{X: 2, Y: 1, Z: 0.5}, Vec3{}, Vec3{Y: 1}, 0, 2 * math.Pi},
		{Vec3{X: 1, Y: 2, Z: 0}, Vec3{}, Vec3{X: 1, Y: 1, Z: 0.3}, 0.4, 3.9},
		{Vec3{X: -1.5, Y: 0, Z: 0.7}, Vec3{Y: 0.5}, Vec3{Y: 1}, 2.5, -0.7},
		{Vec3{X: 1, Y: 1, Z: 1}, Vec3{}, Vec3{Y: 1}, 0.7, 0.7}, // 退化：零张角
	}
	// ArcBounds 的 from/to 是"相对起点 p 的旋转量"：p 对应偏移 0。
	const samples = 40000
	for i, c := range cases {
		box := ArcBounds(c.p, c.pivot, c.axis, c.from, c.to)
		lo, hi := 0.0, math.Abs(c.to-c.from)
		var sampled AABB
		for k := 0; k <= samples; k++ {
			ang := lo + (hi-lo)*float64(k)/samples
			q := RotateAbout(c.p, c.pivot, c.axis, ang)
			// 采样与闭式解走的是两条不同的浮点路径，边界上会有 1 ULP 差异。
			if !box.Expand(1e-9).Contains(q) {
				t.Fatalf("case %d: 闭式盒漏掉采样点 %v（角度 %v）", i, q, ang)
			}
			if k == 0 {
				sampled = q.Bounds()
			}
			sampled = sampled.Union(q.Bounds())
		}
		// 采样越密，采样盒越接近闭式盒；差距应当在采样误差量级内。
		const tol = 1e-6
		if d := box.Min.Distance(sampled.Min); d > tol {
			t.Fatalf("case %d: 闭式盒下界过松，差 %v（%v vs %v）", i, d, box.Min, sampled.Min)
		}
		if d := box.Max.Distance(sampled.Max); d > tol {
			t.Fatalf("case %d: 闭式盒上界过松，差 %v（%v vs %v）", i, d, box.Max, sampled.Max)
		}
	}
}

// TestArcBoundsOnAxis：点在旋转轴上时整段弧退化成一点。
func TestArcBoundsOnAxis(t *testing.T) {
	p := Vec3{Y: 1.5}
	box := ArcBounds(p, Vec3{}, Vec3{Y: 1}, 0, 2)
	if !vecAlmostEq(box.Min, p) || !vecAlmostEq(box.Max, p) {
		t.Fatalf("轴上点的弧盒应为退化点，got %v..%v", box.Min, box.Max)
	}
}

// TestSectorBoundsMatchesSampling：扇形包围盒也必须精确包住扇区内的所有点。
func TestSectorBoundsMatchesSampling(t *testing.T) {
	cases := []Sector{
		{Center: Vec3{X: 1, Z: -2}, From: -1.0, To: 1.5, R0: 0.5, R1: 3},
		{Center: Vec3{Z: 0.4}, From: 2.8, To: -2.6, R0: 0, R1: 2},               // 反向、跨 ±π
		{Center: Vec3{X: -1.2, Z: 0.3}, From: 0, To: 2 * math.Pi, R0: 1, R1: 1}, // 整圈窄环
	}
	const angSteps, radSteps = 2000, 40
	for i, s := range cases {
		box := s.Bounds()
		if !math.IsInf(box.Min.Y, -1) || !math.IsInf(box.Max.Y, 1) {
			t.Fatalf("case %d: 无竖直约束时 Y 应为 ±Inf", i)
		}
		span := s.Span()
		for a := 0; a <= angSteps; a++ {
			ang := s.From + span*float64(a)/angSteps
			for r := 0; r <= radSteps; r++ {
				rad := s.R0 + (s.R1-s.R0)*float64(r)/radSteps
				p := arcStart(s.Center, rad, ang)
				if !box.Expand(1e-9).Contains(p) {
					t.Fatalf("case %d: 扇形盒漏掉 %v（角度 %v 半径 %v）", i, p, ang, rad)
				}
			}
		}
	}
}

func TestSectorContains(t *testing.T) {
	s := Sector{Center: Vec3{}, From: 0, To: math.Pi / 2, R0: 1, R1: 3}
	at := func(r, ang float64) Vec3 { return Vec3{X: r * math.Cos(ang), Z: r * math.Sin(ang)} }

	if !s.Contains(at(2, math.Pi/4), 0) {
		t.Fatal("45° / 2m 应命中")
	}
	if !s.Contains(at(1, 0), 0) || !s.Contains(at(3, math.Pi/2), 0) {
		t.Fatal("边界（内圈、外圈、两端 0°/90°）应命中")
	}
	if s.Contains(at(2, -0.1), 0) || s.Contains(at(2, math.Pi/2+0.1), 0) {
		t.Fatal("角度之外不应命中")
	}
	if s.Contains(at(0.5, math.Pi/4), 0) {
		t.Fatal("内圈之内不应命中")
	}
	if s.Contains(at(4, math.Pi/4), 0) {
		t.Fatal("外圈之外不应命中")
	}
	// 球半径补偿：贴在外圈外一点点，但半径够大 → 应命中
	if !s.Contains(at(3.2, math.Pi/4), 0.3) {
		t.Fatal("半径够大的球应命中（距离带放宽）")
	}
	if s.Contains(at(3.5, math.Pi/4), 0.3) {
		t.Fatal("半径补不到远端时不应命中")
	}
	// 反向张角（To < From）
	rev := Sector{Center: Vec3{}, From: math.Pi / 2, To: 0, R0: 0, R1: 2}
	if !rev.Contains(at(1, math.Pi/4), 0) {
		t.Fatal("反向张角应覆盖两者之间的角度")
	}
	// 竖直带
	band := Sector{Center: Vec3{}, From: 0, To: math.Pi, R0: 0, R1: 5, YMin: 0, YMax: 2}
	if !band.Contains(Vec3{X: 1, Y: 1}, 0) {
		t.Fatal("竖直带内应命中")
	}
	if band.Contains(Vec3{X: 1, Y: 3}, 0) {
		t.Fatal("竖直带外不应命中")
	}
}
