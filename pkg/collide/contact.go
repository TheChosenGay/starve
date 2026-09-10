package collide

import "math"

// Contact 描述两个图元的接触信息。
//
// 约定：Normal 从第二个参数（B）指向第一个参数（A），
// 也就是“把 A 沿 +Normal 推开”即可分离。Depth > 0 表示重叠。
type Contact struct {
	Normal Vec3    // 接触法向（B → A）
	Depth  float64 // 穿透深度
	Point  Vec3    // 接触点（接触区域上的代表点）
}

// ContactSphereSphere 球 a 与球 b 的接触。
func ContactSphereSphere(a, b Sphere) (Contact, bool) {
	// 球 = 内层点 + 半径 R
	return contactFromInner(a.C, b.C, a.R, b.R, Vec3{X: 1})
}

// ContactSphereAABB 球 s 与 AABB b 的接触。
func ContactSphereAABB(s Sphere, b AABB) (Contact, bool) {
	q := ClosestPtPointAABB(s.C, b)
	delta := s.C.Sub(q)
	d := delta.Len()
	if d > 1e-9 {
		if d > s.R {
			return Contact{}, false
		}
		return Contact{Normal: delta.Scale(1 / d), Depth: s.R - d, Point: q}, true
	}
	// 球心在盒内：沿穿透最浅的面推出去
	n := outsideNormal(s.C, b)
	axis := dominantAxis(n)
	var faceDist float64
	if n.At(axis) > 0 {
		faceDist = b.Max.At(axis) - s.C.At(axis)
	} else {
		faceDist = s.C.At(axis) - b.Min.At(axis)
	}
	p := s.C.WithAt(axis, b.Max.At(axis))
	if n.At(axis) < 0 {
		p = s.C.WithAt(axis, b.Min.At(axis))
	}
	return Contact{Normal: n, Depth: s.R + faceDist, Point: p}, true
}

// ContactSphereOBB 球 s 与 OBB b 的接触。
func ContactSphereOBB(s Sphere, b OBB) (Contact, bool) {
	q := ClosestPtPointOBB(s.C, b)
	delta := s.C.Sub(q)
	d := delta.Len()
	if d > 1e-9 {
		if d > s.R {
			return Contact{}, false
		}
		return Contact{Normal: delta.Scale(1 / d), Depth: s.R - d, Point: q}, true
	}
	// 球心在盒内：在局部坐标里找穿透最浅的轴
	local := s.C.Sub(b.C)
	bestAxis, bestSign := 0, 1.0
	bestGap := math.Inf(1)
	for i := 0; i < 3; i++ {
		di := local.Dot(b.U[i])
		if gap := b.E[i] - di; gap < bestGap {
			bestGap, bestAxis, bestSign = gap, i, 1
		}
		if gap := b.E[i] + di; gap < bestGap {
			bestGap, bestAxis, bestSign = gap, i, -1
		}
	}
	n := b.U[bestAxis].Scale(bestSign)
	return Contact{Normal: n, Depth: s.R + bestGap, Point: q}, true
}

// ContactSphereCapsule 球 s 与胶囊 b 的接触。
func ContactSphereCapsule(s Sphere, b Capsule) (Contact, bool) {
	// 球 = 点 + R，胶囊 = 线段 + R
	q := ClosestPtPointSegment(s.C, b.A, b.B)
	return contactFromInner(s.C, q, s.R, b.R, perpTo(b.B.Sub(b.A)))
}

// ContactCapsuleCapsule 胶囊 a 与胶囊 b 的接触。
func ContactCapsuleCapsule(a, b Capsule) (Contact, bool) {
	// 胶囊 = 线段 + R：先求两轴线最近点，再套半径和
	_, _, c1, c2, _ := ClosestPtSegmentSegment(a.A, a.B, b.A, b.B)
	return contactFromInner(c1, c2, a.R, b.R, perpTo(a.B.Sub(a.A)))
}

// ContactCapsuleOBB 胶囊 a 与 OBB b 的接触。
// 胶囊 = 线段 + 半径，所以归结为“线段到盒的距离”再套半径。
// 武器（细长盒）打到身体（胶囊）走的就是它。
func ContactCapsuleOBB(a Capsule, b OBB) (Contact, bool) {
	pCapsule, pBox, d2 := ClosestPtSegmentOBB(a.A, a.B, b)
	d := math.Sqrt(d2)
	if d > a.R {
		return Contact{}, false
	}
	n := safeNormal(pCapsule.Sub(pBox)) // 从盒指向胶囊轴
	if d < 1e-9 {
		n = obbFaceNormalAt(b, pCapsule) // 轴线穿过盒内：取最浅的面
	}
	pOnCapsule := pCapsule.Sub(n.Scale(a.R))
	return Contact{
		Normal: n,
		Depth:  a.R - d,
		Point:  pBox.Add(pOnCapsule).Scale(0.5),
	}, true
}

// obbFaceNormalAt 返回把点 p（在盒内）推离盒的方向：取穿透最浅的那个面。
func obbFaceNormalAt(b OBB, p Vec3) Vec3 {
	local := b.ToLocal(p)
	bestAxis, bestSign := 0, 1.0
	bestGap := math.Inf(1)
	for i := 0; i < 3; i++ {
		di := local.At(i)
		if gap := b.E[i] - di; gap < bestGap {
			bestGap, bestAxis, bestSign = gap, i, 1
		}
		if gap := b.E[i] + di; gap < bestGap {
			bestGap, bestAxis, bestSign = gap, i, -1
		}
	}
	return b.U[bestAxis].Scale(bestSign)
}

// contactFromInner 是“带半径形状”共用的接触内核（书 4.5 的球扫掠体积）：
// 已知两个内层图元上的最近点 cA（属于 A）、cB（属于 B），套上各自半径即得接触。
// fallback 用于两内层图元重合、法向退化的情形。
func contactFromInner(cA, cB Vec3, rA, rB float64, fallback Vec3) (Contact, bool) {
	delta := cA.Sub(cB)
	d := delta.Len()
	r := rA + rB
	if d > r {
		return Contact{}, false
	}
	n := safeNormal(delta)
	if d < 1e-9 {
		n = safeNormal(fallback)
	}
	pa := cA.Sub(n.Scale(rA))
	pb := cB.Add(n.Scale(rB))
	return Contact{Normal: n, Depth: r - d, Point: pa.Add(pb).Scale(0.5)}, true
}

// ContactAABBAABB 两个 AABB 的接触：轴对齐盒的最小平移向量。
func ContactAABBAABB(a, b AABB) (Contact, bool) {
	ca, cb := a.Center(), b.Center()
	bestAxis := 0
	bestDepth := math.Inf(1)
	bestSign := 1.0
	for i := 0; i < 3; i++ {
		lo := math.Max(a.Min.At(i), b.Min.At(i))
		hi := math.Min(a.Max.At(i), b.Max.At(i))
		ov := hi - lo
		if ov <= 0 {
			return Contact{}, false
		}
		if ov < bestDepth {
			bestDepth, bestAxis = ov, i
			if ca.At(i) >= cb.At(i) {
				bestSign = 1
			} else {
				bestSign = -1
			}
		}
	}
	var p Vec3
	for i := 0; i < 3; i++ {
		lo := math.Max(a.Min.At(i), b.Min.At(i))
		hi := math.Min(a.Max.At(i), b.Max.At(i))
		p = p.WithAt(i, 0.5*(lo+hi))
	}
	return Contact{Normal: Vec3{}.WithAt(bestAxis, bestSign), Depth: bestDepth, Point: p}, true
}

// dominantAxis 返回向量绝对值最大的分量下标。
func dominantAxis(v Vec3) int {
	axis := 0
	if math.Abs(v.Y) > math.Abs(v.At(axis)) {
		axis = 1
	}
	if math.Abs(v.Z) > math.Abs(v.At(axis)) {
		axis = 2
	}
	return axis
}

// perpTo 返回一个与 v 垂直的单位向量（v 为零时返回 +X）。
func perpTo(v Vec3) Vec3 {
	n := safeNormal(v)
	ref := Vec3{X: 1}
	if math.Abs(n.X) > 0.9 {
		ref = Vec3{Y: 1}
	}
	return safeNormal(n.Cross(ref))
}
