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

// ArcBounds 返回点 p 绕「过 pivot、方向为 axis」的轴从 from 转到 to 所扫过圆弧的包围盒。
//
// 闭式解：两个端点 + 角区间内经过的 4 个坐标极值角，不采样、不近似。
func ArcBounds(p, pivot, axis Vec3, from, to float64) AABB {
	if to < from {
		from, to = to, from
	}
	span := to - from
	box := AABBFromPoints(p, RotateAbout(p, pivot, axis, span))
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
			if off := mod2pi(cand); off <= span {
				box = box.Union(RotateAbout(p, pivot, k, off).Bounds())
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

// Sector 是水平面上的扇形查询体积：以 Center 为心，角度从 From 张到 To，
// 水平距离落在 [R0, R1] 内。角度以 +X 为 0、绕 +Y 增加（Z 是另一个水平轴）。
//
// Sector 不是图元：它只用于查询（挥砍、范围技能），不参与碰撞代数。
// 因此它可以是非凸的（R0 > 0 时是环形、From≠To 时是楔形）而不引入任何几何代价。
//
// 张角按最短弧解释（≤ 180°）——挥砍和技能锥都不会超过半圈。
type Sector struct {
	Center     Vec3
	From, To   float64
	R0, R1     float64
	YMin, YMax float64 // 竖直范围；YMax <= YMin 表示竖直方向不设限
}

// Planar 表示扇形在竖直方向不设限。
func (s Sector) Planar() bool { return s.YMax <= s.YMin }

// Span 返回有符号张角（∈ [-π, π]，按最短弧解释）。
func (s Sector) Span() float64 { return wrapPi(s.To - s.From) }

// Bounds 返回扇形的包围盒。
// 水平方向精确（内弧盒 ∪ 外弧盒），不采样；竖直方向无约束时取 ±Inf。
func (s Sector) Bounds() AABB {
	span := s.Span()
	// 扇形的角度用 (cos, sin) 定义在 (X,Z) 平面上，即角度增大方向是 +X → +Z，
	// 对应绕 +Y 的负向旋转——所以这里传 -Y 作轴，与 arcStart / Contains 保持一致。
	axis := Vec3{Y: -1}
	outer := ArcBounds(arcStart(s.Center, s.R1, s.From), s.Center, axis, 0, span)
	inner := ArcBounds(arcStart(s.Center, s.R0, s.From), s.Center, axis, 0, span)
	box := outer.Union(inner)
	if s.Planar() {
		box.Min.Y, box.Max.Y = math.Inf(-1), math.Inf(1)
		return box
	}
	box.Min.Y, box.Max.Y = s.YMin, s.YMax
	return box
}

// Contains 判断「以 p 为心、半径 r 的球」是否与扇形相交。
//
// 这是"中等精度"档：距离带用球半径做精确放宽，角度用 asin(r/dist) 近似放宽。
// 对胶囊/球这类目标足够稳；要完全精确需要按图元求扇区最近点。
func (s Sector) Contains(p Vec3, r float64) bool {
	if !s.Planar() && (p.Y+r < s.YMin || p.Y-r > s.YMax) {
		return false
	}
	dx, dz := p.X-s.Center.X, p.Z-s.Center.Z
	dist := math.Hypot(dx, dz)
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
	rel := wrapPi(math.Atan2(dz, dx) - s.From)
	span := s.Span()
	if span >= 0 {
		return rel >= -tol && rel <= span+tol
	}
	return rel <= tol && rel >= span-tol
}

// arcStart 返回扇形在角 a、半径 r 处的弧起点。
func arcStart(center Vec3, r, a float64) Vec3 {
	return Vec3{
		X: center.X + r*math.Cos(a),
		Y: center.Y,
		Z: center.Z + r*math.Sin(a),
	}
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
