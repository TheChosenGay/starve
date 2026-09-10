package collide

import (
	"math"
	"sort"
)

// degenerateEps 用于判断线段是否退化为点（比较平方长度）。
const degenerateEps = 1e-12

// ClosestPtPointPlane 返回平面上离点 p 最近的点（正交投影）。
// 对应 Ericson 5.1.1。
func ClosestPtPointPlane(p Vec3, pl Plane) Vec3 {
	nn := pl.N.Dot(pl.N)
	if nn == 0 {
		return p
	}
	t := (pl.N.Dot(p) - pl.D) / nn
	return p.Sub(pl.N.Scale(t))
}

// SignedDistancePointPlane 返回点 p 到平面的有符号距离（正值在法向一侧）。
func SignedDistancePointPlane(p Vec3, pl Plane) float64 {
	l := pl.N.Len()
	if l == 0 {
		return 0
	}
	return (pl.N.Dot(p) - pl.D) / l
}

// ClosestPtPointSegment 返回线段 ab 上离点 c 最近的点。
// 对应 Ericson 5.1.2。
func ClosestPtPointSegment(c, a, b Vec3) Vec3 {
	ab := b.Sub(a)
	denom := ab.Dot(ab)
	if denom <= degenerateEps {
		return a
	}
	t := clamp(c.Sub(a).Dot(ab)/denom, 0, 1)
	return a.Add(ab.Scale(t))
}

// SqDistPointSegment 返回点 c 到线段 ab 的平方距离。
// 对应 Ericson 5.1.2.1。
func SqDistPointSegment(a, b, c Vec3) float64 {
	ab := b.Sub(a)
	ac := c.Sub(a)
	bc := c.Sub(b)
	e := ac.Dot(ab)
	if e <= 0 {
		return ac.Dot(ac)
	}
	f := ab.Dot(ab)
	if e >= f {
		return bc.Dot(bc)
	}
	return ac.Dot(ac) - e*e/f
}

// ClosestPtPointAABB 返回 AABB 上（或内）离点 p 最近的点。
// 对应 Ericson 5.1.3。
func ClosestPtPointAABB(p Vec3, b AABB) Vec3 {
	return Vec3{X: clamp(p.X, b.Min.X, b.Max.X), Y: clamp(p.Y, b.Min.Y, b.Max.Y), Z: clamp(p.Z, b.Min.Z, b.Max.Z)}
}

// SqDistPointAABB 返回点 p 到 AABB 的平方距离。
// 对应 Ericson 5.1.3.1。
func SqDistPointAABB(p Vec3, b AABB) float64 {
	var sq float64
	if p.X < b.Min.X {
		d := b.Min.X - p.X
		sq += d * d
	} else if p.X > b.Max.X {
		d := p.X - b.Max.X
		sq += d * d
	}
	if p.Y < b.Min.Y {
		d := b.Min.Y - p.Y
		sq += d * d
	} else if p.Y > b.Max.Y {
		d := p.Y - b.Max.Y
		sq += d * d
	}
	if p.Z < b.Min.Z {
		d := b.Min.Z - p.Z
		sq += d * d
	} else if p.Z > b.Max.Z {
		d := p.Z - b.Max.Z
		sq += d * d
	}
	return sq
}

// ClosestPtPointOBB 返回 OBB 上（或内）离点 p 最近的点。
// 对应 Ericson 5.1.4。
func ClosestPtPointOBB(p Vec3, b OBB) Vec3 {
	d := p.Sub(b.C)
	q := b.C
	for i := 0; i < 3; i++ {
		dist := clamp(d.Dot(b.U[i]), -b.E[i], b.E[i])
		q = q.Add(b.U[i].Scale(dist))
	}
	return q
}

// SqDistPointOBB 返回点 p 到 OBB 的平方距离。
// 对应 Ericson 5.1.4.1。
func SqDistPointOBB(p Vec3, b OBB) float64 {
	v := p.Sub(b.C)
	var sq float64
	for i := 0; i < 3; i++ {
		d := v.Dot(b.U[i])
		var excess float64
		if d < -b.E[i] {
			excess = d + b.E[i]
		} else if d > b.E[i] {
			excess = d - b.E[i]
		}
		sq += excess * excess
	}
	return sq
}

// ClosestPtPointTriangle 返回三角形 abc 上离点 p 最近的点。
// 采用 Ericson 5.1.5 的优化版本（用点积判 Voronoi 区域，无叉积）。
func ClosestPtPointTriangle(p, a, b, c Vec3) Vec3 {
	ab := b.Sub(a)
	ac := c.Sub(a)
	ap := p.Sub(a)
	d1 := ab.Dot(ap)
	d2 := ac.Dot(ap)
	if d1 <= 0 && d2 <= 0 { // 顶点 A 区域
		return a
	}

	bp := p.Sub(b)
	d3 := ab.Dot(bp)
	d4 := ac.Dot(bp)
	if d3 >= 0 && d4 <= d3 { // 顶点 B 区域
		return b
	}

	vc := d1*d4 - d3*d2
	if vc <= 0 && d1 >= 0 && d3 <= 0 { // 边 AB 区域
		v := d1 / (d1 - d3)
		return a.Add(ab.Scale(v))
	}

	cp := p.Sub(c)
	d5 := ab.Dot(cp)
	d6 := ac.Dot(cp)
	if d6 >= 0 && d5 <= d6 { // 顶点 C 区域
		return c
	}

	vb := d5*d2 - d1*d6
	if vb <= 0 && d2 >= 0 && d6 <= 0 { // 边 AC 区域
		w := d2 / (d2 - d6)
		return a.Add(ac.Scale(w))
	}

	va := d3*d6 - d5*d4
	if va <= 0 && (d4-d3) >= 0 && (d5-d6) >= 0 { // 边 BC 区域
		w := (d4 - d3) / ((d4 - d3) + (d5 - d6))
		return b.Add(c.Sub(b).Scale(w))
	}

	// 面区域：用重心坐标投影到三角形内部
	denom := 1 / (va + vb + vc)
	v := vb * denom
	w := vc * denom
	return a.Add(ab.Scale(v)).Add(ac.Scale(w))
}

// SqDistPointTriangle 返回点 p 到三角形 abc 的平方距离。
func SqDistPointTriangle(p, a, b, c Vec3) float64 {
	return ClosestPtPointTriangle(p, a, b, c).DistanceSq(p)
}

// ClosestPtSegmentSegment 返回两条线段的最近点对。
// 参数 p1-q1 为线段 S1，p2-q2 为线段 S2；返回 S1 上的参数 s、S2 上的参数 t、
// 对应最近点 c1、c2，以及平方距离。对应 Ericson 5.1.9。
//
// 注意：仅当两线段的最近点确实落在各自线段上时，直线解法才适用；
// 这里采用 Ericson 的 clamp 迭代，能正确处理端点情形。
func ClosestPtSegmentSegment(p1, q1, p2, q2 Vec3) (s, t float64, c1, c2 Vec3, distSq float64) {
	d1 := q1.Sub(p1)
	d2 := q2.Sub(p2)
	r := p1.Sub(p2)
	a := d1.Dot(d1)
	e := d2.Dot(d2)
	f := d2.Dot(r)

	// 两条线段都退化为点
	if a <= degenerateEps && e <= degenerateEps {
		return 0, 0, p1, p2, p1.DistanceSq(p2)
	}
	// 第一条线段退化为点
	if a <= degenerateEps {
		s = 0
		t = clamp(f/e, 0, 1)
		return s, t, p1, p2.Add(d2.Scale(t)), p1.DistanceSq(p2.Add(d2.Scale(t)))
	}

	c := d1.Dot(r)
	// 第二条线段退化为点
	if e <= degenerateEps {
		t = 0
		s = clamp(-c/a, 0, 1)
		return s, t, p1.Add(d1.Scale(s)), p2, p1.Add(d1.Scale(s)).DistanceSq(p2)
	}

	b := d1.Dot(d2)
	denom := a*e - b*b // 恒非负
	if denom != 0 {
		s = clamp((b*f-c*e)/denom, 0, 1)
	} else {
		s = 0 // 两线段平行，任取 s=0
	}
	t = (b*s + f) / e
	if t < 0 {
		t = 0
		s = clamp(-c/a, 0, 1)
	} else if t > 1 {
		t = 1
		s = clamp((b-c)/a, 0, 1)
	}

	c1 = p1.Add(d1.Scale(s))
	c2 = p2.Add(d2.Scale(t))
	return s, t, c1, c2, c1.DistanceSq(c2)
}

// ClosestPtSegmentOBB 返回线段 ab 与 OBB 的最近点对，以及平方距离。
//
// 做法：把线段变换到盒的局部坐标系（盒变成以原点为中心、半宽 E 的 AABB），
// 则点到盒的平方距离为 g(t) = Σ_i max(0, |p_i(t)| − E_i)²，其中 p(t) = la + t·d。
// g 是分段二次函数，分段点即 p_i(t) = ±E_i；在每个区间内“越界轴集合”固定，
// 于是可解析求出该区间的最小值点。逐段取最小即得精确解（非迭代、非近似）。
func ClosestPtSegmentOBB(a, b Vec3, box OBB) (Vec3, Vec3, float64) {
	la := box.ToLocal(a)
	lb := box.ToLocal(b)
	d := lb.Sub(la)
	e := box.E

	// 1) 收集分段点：线段与六张面片所在平面的交点
	ts := []float64{0, 1}
	for i := 0; i < 3; i++ {
		di := d.At(i)
		if math.Abs(di) < 1e-12 {
			continue // 该轴平行于面片，无交点
		}
		for _, s := range [2]float64{e[i], -e[i]} {
			t := (s - la.At(i)) / di
			if t > 0 && t < 1 {
				ts = append(ts, t)
			}
		}
	}
	sort.Float64s(ts)

	// 2) 逐区间求 g 的最小值
	bestT, bestG := 0.0, math.Inf(1)
	for k := 0; k+1 < len(ts); k++ {
		t0, t1 := ts[k], ts[k+1]
		if t1-t0 < 1e-15 {
			continue
		}
		tm := 0.5 * (t0 + t1)
		var sa, sb, sc float64 // g(t) = sa·t² + 2·sb·t + sc
		for i := 0; i < 3; i++ {
			pi := la.At(i) + tm*d.At(i)
			var sigma float64
			switch {
			case pi > e[i]:
				sigma = 1
			case pi < -e[i]:
				sigma = -1
			default:
				continue // 该轴未越界
			}
			A := sigma * d.At(i)
			B := sigma*la.At(i) - e[i]
			sa += A * A
			sb += A * B
			sc += B * B
		}
		tStar := tm
		if sa > 1e-18 {
			tStar = clamp(-sb/sa, t0, t1) // 区间内的解析最小值点
		}
		if g := sa*tStar*tStar + 2*sb*tStar + sc; g < bestG {
			bestG, bestT = g, tStar
		}
	}

	// 3) 还原世界坐标的最近点对
	p := la.Add(d.Scale(bestT))
	q := Vec3{X: clamp(p.X, -e[0], e[0]), Y: clamp(p.Y, -e[1], e[1]), Z: clamp(p.Z, -e[2], e[2])}
	wp, wq := box.ToWorld(p), box.ToWorld(q)
	return wp, wq, wp.DistanceSq(wq)
}

// SqDistSegmentOBB 返回线段 ab 到 OBB 的平方距离。
func SqDistSegmentOBB(a, b Vec3, box OBB) float64 {
	_, _, d2 := ClosestPtSegmentOBB(a, b, box)
	return d2
}
