package collide

import "math"

// obbAxisEpsSq 判断叉积轴是否退化（两棱近平行）。平方比较，避免开方。
const obbAxisEpsSq = 1e-12

// obbRadius 返回盒 b 在单位轴 n 上的投影半径：e0|u0·n| + e1|u1·n| + e2|u2·n|。
func obbRadius(b OBB, n Vec3) float64 {
	return b.E[0]*math.Abs(n.Dot(b.U[0])) +
		b.E[1]*math.Abs(n.Dot(b.U[1])) +
		b.E[2]*math.Abs(n.Dot(b.U[2]))
}

// eachObbAxis 遍历两个 OBB 的候选分离轴，共 15 个：
// A 的 3 个面法向、B 的 3 个面法向、以及两边方向两两叉积的 9 个轴（Ericson 4.4.1 / 5.2.1）。
//
// 两棱近平行时叉积退化为零向量，该轴不携带方向信息：直接跳过。
// 这与书里“给 AbsR 加 epsilon 使其保守化”是同一个意图——宁可多判相交，
// 也不要把零向量误当成分离轴（4.4.2）。
// fn 返回 false 表示调用方要求提前结束（例如已经找到分离轴）。
func eachObbAxis(a, b OBB, fn func(axis Vec3) bool) {
	for i := 0; i < 3; i++ {
		if !fn(a.U[i]) {
			return
		}
	}
	for j := 0; j < 3; j++ {
		if !fn(b.U[j]) {
			return
		}
	}
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			c := a.U[i].Cross(b.U[j])
			if c.LenSq() < obbAxisEpsSq {
				continue // 近平行棱：叉积退化，跳过该轴
			}
			if !fn(c) {
				return
			}
		}
	}
}

// TestOBBOBB 判断两个 OBB 是否相交（含相切）。
func TestOBBOBB(a, b OBB) bool {
	hit := true
	eachObbAxis(a, b, func(axis Vec3) bool {
		n := axis.Normalized()
		d := math.Abs(b.C.Sub(a.C).Dot(n))
		if d > obbRadius(a, n)+obbRadius(b, n) {
			hit = false
			return false // 找到分离轴
		}
		return true
	})
	return hit
}

// ContactOBBOBB 求两个 OBB 的接触：15 个轴里重叠最小者即接触法向，重叠量即穿透深度。
// 法向约定与其它 Contact* 一致：从 b 指向 a（把 a 沿 +Normal 推开即可分离）。
func ContactOBBOBB(a, b OBB) (Contact, bool) {
	best := Contact{}
	bestDepth := math.Inf(1)
	hit := true
	eachObbAxis(a, b, func(axis Vec3) bool {
		n := axis.Normalized()
		rA, rB := obbRadius(a, n), obbRadius(b, n)
		dSigned := a.C.Sub(b.C).Dot(n) // 从 b 指向 a 的有符号距离
		if math.Abs(dSigned) > rA+rB {
			hit = false
			return false // 找到分离轴
		}
		depth := rA + rB - math.Abs(dSigned)
		if depth < bestDepth {
			bestDepth = depth
			dir := n
			if dSigned < 0 {
				dir = n.Neg() // 保证法向从 b 指向 a
			}
			best = Contact{Normal: dir, Depth: depth, Point: obbContactPoint(a, b, dir, rA, rB)}
		}
		return true
	})
	if !hit {
		return Contact{}, false
	}
	return best, true
}

// obbContactPoint 取两盒在法向 dir 上重叠区间的中点，横向位置取两盒中心的中点。
// 这是一个“位于重叠区域内”的代表点；需要精确接触面时要再做参考面裁剪。
func obbContactPoint(a, b OBB, dir Vec3, rA, rB float64) Vec3 {
	lo := a.C.Dot(dir) - rA // a 在 -dir 侧的表面
	hi := b.C.Dot(dir) + rB // b 在 +dir 侧的表面
	m := 0.5 * (lo + hi)
	mid := a.C.Add(b.C).Scale(0.5)
	return mid.Add(dir.Scale(m - mid.Dot(dir)))
}
