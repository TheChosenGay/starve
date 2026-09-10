package collide

import "math"

// rayEps 用来判断射线方向分量是否近似为零。
const rayEps = 1e-12

// IntersectRaySphere 求射线（原点 o、单位方向 dir、最远 maxDist）与球的首次相交。
// maxDist 可为 math.Inf(1)（无限远）。对应 Ericson 5.3.2。
func IntersectRaySphere(o, dir Vec3, maxDist float64, s Sphere) Hit {
	m := o.Sub(s.C)
	b := m.Dot(dir)
	c := m.Dot(m) - s.R*s.R
	if c > 0 && b > 0 {
		return Hit{} // 起点在球外且背离球
	}
	discr := b*b - c
	if discr < 0 {
		return Hit{}
	}
	t := -b - math.Sqrt(discr)
	inside := false
	if t < 0 {
		t, inside = 0, true // 起点已在球内
	}
	if t > maxDist {
		return Hit{}
	}
	p := o.Add(dir.Scale(t))
	n := safeNormal(p.Sub(s.C))
	if inside {
		n = n.Neg()
	}
	return Hit{Hit: true, Dist: t, Point: p, Normal: n}
}

// IntersectRayAABB 求射线（原点 o、单位方向 dir、最远 maxDist）与 AABB 的首次相交。
// 用 slab 法，对应 Ericson 5.3.3。
func IntersectRayAABB(o, dir Vec3, maxDist float64, b AABB) Hit {
	tmin, tmax := 0.0, maxDist
	hitAxis := -1
	hitSign := 0.0
	for i := 0; i < 3; i++ {
		d := dir.At(i)
		lo, hi := b.Min.At(i), b.Max.At(i)
		o0 := o.At(i)
		if math.Abs(d) < rayEps {
			if o0 < lo || o0 > hi {
				return Hit{} // 平行且在 slab 之外
			}
			continue
		}
		ood := 1 / d
		t1 := (lo - o0) * ood
		t2 := (hi - o0) * ood
		sign := -1.0
		if t1 > t2 {
			t1, t2 = t2, t1
			sign = 1.0
		}
		if t1 > tmin {
			tmin, hitAxis, hitSign = t1, i, sign
		}
		if t2 < tmax {
			tmax = t2
		}
		if tmin > tmax {
			return Hit{}
		}
	}
	p := o.Add(dir.Scale(tmin))
	var n Vec3
	if hitAxis >= 0 {
		n = Vec3{}.WithAt(hitAxis, hitSign)
	}
	return Hit{Hit: true, Dist: tmin, Point: p, Normal: n}
}
