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
	// p 是角度 0 处的姿态，弧覆盖 [from, to]。
	const samples = 40000
	for i, c := range cases {
		box := ArcBounds(c.p, c.pivot, c.axis, c.from, c.to)
		lo, hi := math.Min(c.from, c.to), math.Max(c.from, c.to)
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

// TestSectorBoundsMatchesSampling：扇形包围盒必须精确包住扇区内的所有点
// （含沿轴向 ±Thickness 的那一层），也必须足够紧——采样与闭式解走的是两条路径。
func TestSectorBoundsMatchesSampling(t *testing.T) {
	cases := []Sector{
		{Center: Vec3{X: 1, Z: -2}, From: -1.0, To: 1.5, R0: 0.5, R1: 3, Thickness: 1.2},
		{Center: Vec3{Z: 0.4}, From: 2.8, To: -2.6, R0: 0, R1: 2, Thickness: 0.8},                                          // 反向、跨 ±π
		{Center: Vec3{X: -1.2, Z: 0.3}, From: 0, To: 2 * math.Pi, R0: 1, R1: 1, Thickness: 2},                              // 整圈窄环
		{Center: Vec3{Y: 1.2}, Axis: Vec3{Z: -1}, Ref: Vec3{X: 1}, From: 1.0, To: -0.6, R0: 0.4, R1: 2.6, Thickness: 0.35}, // 竖直劈砍
		{Center: Vec3{Y: 0.5}, Axis: Vec3{X: 1, Y: 1, Z: 0.3}, From: -0.5, To: 1.7, R0: 0.2, R1: 1.8, Thickness: 0.6},      // 斜轴
	}
	const angSteps, radSteps, axialSteps = 600, 24, 4
	for i, s := range cases {
		box := s.Bounds()
		n := s.axis()
		span, half := s.Span(), s.thick()
		var sampled AABB
		first := true
		for a := 0; a <= angSteps; a++ {
			ang := s.From + span*float64(a)/angSteps
			for r := 0; r <= radSteps; r++ {
				rad := s.R0 + (s.R1-s.R0)*float64(r)/radSteps
				base := s.at(rad, ang)
				for k := -axialSteps; k <= axialSteps; k++ {
					p := base.Add(n.Scale(half * float64(k) / axialSteps))
					if !box.Expand(1e-9).Contains(p) {
						t.Fatalf("case %d: 扇形盒漏掉 %v（角度 %v 半径 %v 轴向 %v）", i, p, ang, rad, k)
					}
					if first {
						sampled, first = p.Bounds(), false
					}
					sampled = sampled.Union(p.Bounds())
				}
			}
		}
		// 紧致性：采样盒已经覆盖轴向的全部范围与内/外弧端点，差距应当在采样误差量级内。
		const tol = 2e-3
		if d := box.Min.Distance(sampled.Min); d > tol {
			t.Fatalf("case %d: 下界过松，差 %v（%v vs %v）", i, d, box.Min, sampled.Min)
		}
		if d := box.Max.Distance(sampled.Max); d > tol {
			t.Fatalf("case %d: 上界过松，差 %v（%v vs %v）", i, d, box.Max, sampled.Max)
		}
	}
}

func TestSectorContains(t *testing.T) {
	s := Sector{Center: Vec3{}, From: 0, To: math.Pi / 2, R0: 1, R1: 3, Thickness: 5}
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
	rev := Sector{Center: Vec3{}, From: math.Pi / 2, To: 0, R0: 0, R1: 2, Thickness: 5}
	if !rev.Contains(at(1, math.Pi/4), 0) {
		t.Fatal("反向张角应覆盖两者之间的角度")
	}
	// 轴向半宽（轴 = +Y，所以这就是"竖直方向的分层"）
	band := Sector{Center: Vec3{Y: 1}, From: 0, To: math.Pi, R0: 0, R1: 5, Thickness: 1}
	if !band.Contains(Vec3{X: 1, Y: 1}, 0) {
		t.Fatal("轴向层内应命中")
	}
	if band.Contains(Vec3{X: 1, Y: 3}, 0) {
		t.Fatal("超出轴向半宽不应命中")
	}
	if !band.Contains(Vec3{X: 1, Y: 3}, 1.5) {
		t.Fatal("球半径补偿后应命中")
	}
}

// TestSectorVerticalChop：竖直劈砍——弧线在竖直平面里，正角度朝上。
// 玩家在原点、朝 +X，所以旋转轴 = +Y×朝向 = -Z。
func TestSectorVerticalChop(t *testing.T) {
	face := Vec3{X: 1}
	axis := YAxis.Cross(face) // +Y × +X = -Z
	chop := Sector{
		Center:    Vec3{Y: 1.2}, // 挥砍绕胸口
		Axis:      axis,
		Ref:       face, // 角度 0 = 正前方（水平）
		From:      1.0,  // 斜上举起
		To:        -0.8, // 斜下收刀
		R0:        0.4,
		R1:        2.4,
		Thickness: 0.4, // 左右半宽
	}
	// 正前方偏下一点（角度 -20°、距离 1.5）：在刀路里
	hit := Vec3{X: 1.5 * math.Cos(0.35), Y: 1.2 - 1.5*math.Sin(0.35), Z: 0}
	if !chop.Contains(hit, 0) {
		t.Fatalf("正前方斜下的目标应命中: %v", hit)
	}
	// 举过头顶之后（角度 +80°）在刀路外
	high := Vec3{X: 0.4, Y: 1.2 + 2.0, Z: 0}
	if chop.Contains(high, 0) {
		t.Fatal("举得比刀路更高不应命中")
	}
	// 正下方脚边（角度 -80°）在刀路外
	low := Vec3{X: 0.5, Y: 1.2 - 2.2, Z: 0}
	if chop.Contains(low, 0) {
		t.Fatal("脚下不应命中")
	}
	// 左右偏移超过 Thickness 的侧面目标不应命中（竖直劈砍有厚度，不是无限宽）
	side := Vec3{X: 1.5, Y: 1.0, Z: 1.2}
	if chop.Contains(side, 0) {
		t.Fatal("侧面偏出扇形厚度的目标不应命中")
	}
	if !chop.Contains(side, 0.9) {
		t.Fatal("半径够大的目标应被厚度补偿命中")
	}
	// 背后的目标不应命中
	behind := Vec3{X: -1.5, Y: 1.0, Z: 0}
	if chop.Contains(behind, 0) {
		t.Fatal("背后不应命中")
	}
	// 包围盒必须有限（竖直劈砍的轴向是有界的）
	box := chop.Bounds()
	for _, v := range []float64{box.Min.X, box.Min.Y, box.Min.Z, box.Max.X, box.Max.Y, box.Max.Z} {
		if math.IsInf(v, 0) || math.IsNaN(v) {
			t.Fatalf("包围盒出现非有限值: %v..%v", box.Min, box.Max)
		}
	}
	if !box.Contains(hit) {
		t.Fatal("包围盒应覆盖刀路内的点")
	}
}
