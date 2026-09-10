package collide

import "math"

// SweepSphereSphere 球 s 沿 motion 平移（motion 是位移向量，范围即 |motion|），
// 对静止球 b 求首次接触。闭式解，对应 Ericson 5.5.5。
func SweepSphereSphere(s Sphere, motion Vec3, b Sphere) Hit {
	r := s.R + b.R
	m := s.C.Sub(b.C)
	aLenSq := motion.Dot(motion)

	if m.LenSq() <= r*r {
		n := safeNormal(m) // 初始已重叠
		return Hit{Hit: true, Point: s.C.Sub(n.Scale(s.R)), Normal: n}
	}
	if aLenSq < 1e-18 {
		return Hit{} // 相对静止
	}
	bq := m.Dot(motion)
	if bq >= 0 {
		return Hit{} // 背离
	}
	discr := bq*bq - aLenSq*(m.LenSq()-r*r)
	if discr < 0 {
		return Hit{}
	}
	t := (-bq - math.Sqrt(discr)) / aLenSq
	if t < 0 || t > 1 {
		return Hit{}
	}
	c := s.C.Add(motion.Scale(t))
	n := safeNormal(c.Sub(b.C))
	return Hit{Hit: true, T: t, Dist: t * math.Sqrt(aLenSq), Point: c.Sub(n.Scale(s.R)), Normal: n}
}

// SweepSphereAABB 球 s 沿 motion 平移，对静止 AABB b 求首次接触。
func SweepSphereAABB(s Sphere, motion Vec3, b AABB) Hit {
	t, ok := sweepSphereVsDist(s, motion, s.R, func(c Vec3) float64 {
		return SqDistPointAABB(c, b)
	})
	if !ok {
		return Hit{}
	}
	c := s.C.Add(motion.Scale(t))
	return Hit{
		Hit:    true,
		T:      t,
		Dist:   t * motion.Len(),
		Point:  ClosestPtPointAABB(c, b),
		Normal: outsideNormal(c, b),
	}
}

// SweepSphereCapsule 球 s 沿 motion 平移，对静止胶囊 b 求首次接触。
// 子弹打人（球打胶囊）用的就是它。
func SweepSphereCapsule(s Sphere, motion Vec3, b Capsule) Hit {
	t, ok := sweepSphereVsDist(s, motion, s.R+b.R, func(c Vec3) float64 {
		return SqDistPointSegment(b.A, b.B, c)
	})
	if !ok {
		return Hit{}
	}
	c := s.C.Add(motion.Scale(t))
	q := ClosestPtPointSegment(c, b.A, b.B) // 胶囊轴上的最近点
	n := safeNormal(c.Sub(q))               // 从胶囊指向球心
	// 接触点取两个表面点的中点
	pSphere := c.Sub(n.Scale(s.R))
	pCapsule := q.Add(n.Scale(b.R))
	return Hit{Hit: true, T: t, Dist: t * motion.Len(), Point: pSphere.Add(pCapsule).Scale(0.5), Normal: n}
}

// SweepFirstSphereAABB 把球 s 沿 motion 扫过一组 AABB，返回最早命中的那个（下标）。
func SweepFirstSphereAABB(s Sphere, motion Vec3, boxes []AABB) (idx int, hit Hit, ok bool) {
	idx = -1
	for i := range boxes {
		h := SweepSphereAABB(s, motion, boxes[i])
		if !h.Hit {
			continue
		}
		if idx < 0 || h.T < hit.T {
			idx, hit = i, h
		}
	}
	return idx, hit, idx >= 0
}

// sweepSphereVsDist 是球扫掠的共用内核：球 s 沿 motion 平移，对“到目标形状的平方距离
// 函数 dist2”（必须是 t 的凸函数）求“最早使距离 ≤ rSum”的时刻。
//
// 先定位凸函数的最小值点，再在 [0, tmin] 上二分求最早接触点。
// 比闭式解慢，但短、稳、可复用于 AABB / 胶囊 / 球等任何能给出距离的目标。
func sweepSphereVsDist(s Sphere, motion Vec3, rSum float64, dist2 func(Vec3) float64) (float64, bool) {
	r2 := rSum * rSum
	f := func(t float64) float64 { return dist2(s.C.Add(motion.Scale(t))) }
	if f(0) <= r2 {
		return 0, true // 初始已接触
	}
	tmin := convexMinT(f, 0, 1)
	if f(tmin) > r2 {
		return 0, false // 整段运动都够不着
	}
	lo, hi := 0.0, tmin // f 在 [0,tmin] 上单调不增
	for i := 0; i < 80; i++ {
		mid := 0.5 * (lo + hi)
		if f(mid) <= r2 {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi, true
}

// convexMinT 在 [lo, hi] 上用黄金分割搜索凸函数 f 的最小值点。
func convexMinT(f func(float64) float64, lo, hi float64) float64 {
	const phi = 0.6180339887498949
	for i := 0; i < 80; i++ {
		a := hi - (hi-lo)*phi
		b := lo + (hi-lo)*phi
		if f(a) < f(b) {
			hi = b
		} else {
			lo = a
		}
	}
	return 0.5 * (lo + hi)
}
