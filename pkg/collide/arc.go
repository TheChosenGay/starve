package collide

import "math"

// YAxis 是俯视世界的竖直轴（扇形与圆弧都绕它张开）。
var YAxis = Vec3{Y: 1}

// RotateAbout 把点 p 绕「过 pivot、方向为 axis」的轴旋转 angle 弧度（右手定则）。
// axis 为零向量或 angle 为 0 时返回 p。
func RotateAbout(p, pivot, axis Vec3, angle float64) Vec3 {
	if angle == 0 {
		return p
	}
	k := axis.Normalized()
	if k.IsZero(1e-15) {
		return p
	}
	v := p.Sub(pivot)
	c, s := math.Cos(angle), math.Sin(angle)
	// 把 v 拆成轴向分量与垂直分量：只有垂直分量在转，轴向分量原样加回去。
	// 比直接用 Rodrigues 的 (1-cosθ) 形式少一次乘加，也让轴向坐标逐位不变
	// （用 Rodrigues 时轴上偏移点会引入 1e-16 级误差，足以让边界判定翻车）。
	axial := k.Scale(k.Dot(v))
	radial := v.Sub(axial)
	rot := axial.Add(radial.Scale(c)).Add(k.Cross(radial).Scale(s))
	return pivot.Add(rot)
}

// ArcBounds 返回点 p 绕「过 pivot、方向为 axis」的轴旋转、角度覆盖 [from, to] 时
// （p 是角度 0 处的姿态）所扫过圆弧的包围盒。
//
// 闭式解：两个端点 + 区间内经过的坐标极值角，不采样、不近似。
func ArcBounds(p, pivot, axis Vec3, from, to float64) AABB {
	if to < from {
		from, to = to, from
	}
	span := to - from
	box := AABBFromPoints(RotateAbout(p, pivot, axis, from), RotateAbout(p, pivot, axis, to))
	if span == 0 {
		return box
	}
	k := axis.Normalized()
	if k.IsZero(1e-15) {
		return box
	}
	r := p.Sub(pivot)
	u, v := orthoBasis(k)
	a0, b0 := r.Dot(u), r.Dot(v)
	if math.Hypot(a0, b0) < 1e-12 {
		return box // 点在轴上：整段弧退化成一个点
	}
	// 旋转后的点在 (u,v,n) 基下是 (a0·cosθ − b0·sinθ, a0·sinθ + b0·cosθ, c)，
	// 于是任一世界坐标轴 e 上的分量是 f(θ) = A·cosθ + B·sinθ + C：
	//   A = e·(a0·u + b0·v)，B = e·(−b0·u + a0·v)，C = e·(c·n)（常数）
	// 极值在 θ = atan2(B, A)（极大）与 θ + π（极小）。注意这里必须逐世界轴求，
	// 不能只取 u/v 两个方向的极值——轴向与世界轴不平行时两者不是同一个角。
	for _, e := range [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}} {
		eu, ev := e.Dot(u), e.Dot(v)
		A := eu*a0 + ev*b0
		B := -eu*b0 + ev*a0
		if math.Hypot(A, B) < 1e-15 {
			continue // 该坐标不随角度变化
		}
		theta := math.Atan2(B, A)
		for _, cand := range [2]float64{theta, theta + math.Pi} {
			// 候选角是相对 p（角度 0）的偏移，需要落在 [from, to] 内才计入。
			if off := mod2pi(cand - from); off <= span {
				box = box.Union(RotateAbout(p, pivot, k, from+off).Bounds())
			}
		}
	}
	return box
}

// orthoBasis 返回与 n 正交的两个单位向量（构成平面内的一组基）。
func orthoBasis(n Vec3) (Vec3, Vec3) {
	n = safeNormal(n)
	ref := Vec3{X: 1}
	if math.Abs(n.X) > 0.9 {
		ref = Vec3{Y: 1}
	}
	u := safeNormal(n.Cross(ref))
	return u, n.Cross(u)
}

// Sector 是绕任意轴张开的扇形查询体积，挥砍与技能锥都用它。
//
// 以 Center 为心，绕 Axis 从 From 张到 To；到轴线的距离落在 [R0, R1]；
// 沿轴向（垂直于扇形所在平面）的半宽不超过 Thickness。角度 0 的方向是 Ref
// 在扇形平面上的投影；「轴 +Y、参考 +X」时角度仍在 XZ 平面里从 +X 转向 +Z。
//
// 两个常见姿势：
//
//	水平挥砍 / 技能锥：Axis = +Y（零值默认），Ref = 朝向，From/To = 左右张角
//	竖直劈砍：        Axis = +Y×朝向（左右方向，正角度朝上），Ref = 朝向，
//	                  From = +1.0（斜上举起）、To = -0.8（斜下收刀）
//
// Sector 不是图元：它只用于查询，不参与碰撞代数，因此可以非凸（R0 > 0 时是环形）
// 而不引入任何几何代价。张角按最短弧解释（≤ 180°）——挥砍和技能锥都不会超过半圈。
type Sector struct {
	Center    Vec3
	Axis      Vec3    // 旋转轴；零向量 = +Y
	Ref       Vec3    // 角度 0 的参考方向；零向量 = +X
	From, To  float64 // 角度区间（弧度）
	R0, R1    float64 // 到轴线的距离带
	Thickness float64 // 沿轴向的半宽；<= 0 时按 R1 处理（扇形在轴向上总是有界的）
}

// Span 返回有符号张角（∈ [-π, π]，按最短弧解释）。
func (s Sector) Span() float64 { return wrapPi(s.To - s.From) }

// axis 返回归一化的旋转轴（零向量时取 +Y）。
func (s Sector) axis() Vec3 {
	if n := s.Axis.Normalized(); !n.IsZero(1e-15) {
		return n
	}
	return YAxis
}

// basis 返回扇形平面内的正交基：u 是角度 0 的方向，w 是角度 +90° 的方向。
// 约定 w = u × n —— 角度增大对应绕 -n 旋转，与 ArcBounds 的旋转方向对齐。
func (s Sector) basis() (Vec3, Vec3) {
	n := s.axis()
	u := s.Ref.Sub(n.Scale(s.Ref.Dot(n))).Normalized()
	if u.IsZero(1e-15) {
		u = Vec3{X: 1}.Sub(n.Scale(n.X)).Normalized()
	}
	if u.IsZero(1e-15) {
		u = Vec3{Z: 1}.Sub(n.Scale(n.Z)).Normalized()
	}
	return u, u.Cross(n)
}

// thick 返回有效的轴向半宽。
func (s Sector) thick() float64 {
	if s.Thickness > 0 {
		return s.Thickness
	}
	if s.R1 > 0 {
		return s.R1
	}
	return 1
}

// at 返回扇形平面内、半径 r、角度 angle 处的点。
func (s Sector) at(r, angle float64) Vec3 {
	u, w := s.basis()
	return s.Center.Add(u.Scale(r * math.Cos(angle))).Add(w.Scale(r * math.Sin(angle)))
}

// Bounds 返回扇形的包围盒，精确且不采样：
// 平面内取「内弧盒 ∪ 外弧盒」（闭式解），再沿轴向按各坐标轴上的投影撑开。
func (s Sector) Bounds() AABB {
	span := s.Span()
	n := s.axis()
	outer := ArcBounds(s.at(s.R1, s.From), s.Center, n.Neg(), 0, span)
	inner := ArcBounds(s.at(s.R0, s.From), s.Center, n.Neg(), 0, span)
	box := outer.Union(inner)
	// 轴向分量与平面内分量互相独立，所以各轴分别外扩 t·|nᵢ| 就是精确包围盒。
	ext := n.Abs().Scale(s.thick())
	return AABB{Min: box.Min.Sub(ext), Max: box.Max.Add(ext)}
}

// Contains 判断「以 p 为心、半径 r 的球」是否与扇形相交。
//
// 这是"中等精度"档：距离带与轴向用球半径做精确放宽，角度用 asin(r/dist) 近似放宽。
// 对胶囊/球这类目标足够稳；要完全精确需要按图元求扇区最近点。
func (s Sector) Contains(p Vec3, r float64) bool {
	n := s.axis()
	off := p.Sub(s.Center)
	axial := off.Dot(n)
	if math.Abs(axial) > s.thick()+r {
		return false // 偏出扇形所在的那一层
	}
	radial := off.Sub(n.Scale(axial))
	dist := radial.Len()
	if dist > s.R1+r {
		return false // 太远
	}
	if dist+r < s.R0 {
		return false // 整颗球都在内圈里
	}
	if dist < 1e-9 {
		return true // 球心落在扇心上：距离带已判定
	}
	tol := math.Pi
	if r < dist {
		tol = math.Asin(r / dist)
	}
	u, w := s.basis()
	rel := wrapPi(math.Atan2(radial.Dot(w), radial.Dot(u)) - s.From)
	span := s.Span()
	if span >= 0 {
		return rel >= -tol && rel <= span+tol
	}
	return rel <= tol && rel >= span-tol
}

// mod2pi 把角度归一到 [0, 2π)。
func mod2pi(a float64) float64 {
	a = math.Mod(a, 2*math.Pi)
	if a < 0 {
		a += 2 * math.Pi
	}
	return a
}

// wrapPi 把角度归一到 (-π, π]。
func wrapPi(a float64) float64 {
	a = math.Mod(a, 2*math.Pi)
	if a > math.Pi {
		a -= 2 * math.Pi
	}
	if a <= -math.Pi {
		a += 2 * math.Pi
	}
	return a
}
