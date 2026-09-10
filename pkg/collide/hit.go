package collide

// Hit 是射线 / 扫掠的命中结果。未命中时零值的 Hit.Hit == false。
type Hit struct {
	Hit    bool    // 是否命中
	Dist   float64 // 沿运动/方向的绝对距离
	T      float64 // 沿 motion 的比例 [0,1]，仅有限扫掠有意义；射线投射时为 0
	Point  Vec3    // 首次接触点
	Normal Vec3    // 接触法向，指向运动体（便于反射 / 推出）
}

// safeNormal 归一化；零向量时退化为 +X，避免 NaN。
func safeNormal(v Vec3) Vec3 {
	if v.LenSq() < 1e-18 {
		return Vec3{X: 1}
	}
	return v.Normalized()
}

// outsideNormal 返回把点 c 推离盒 b 的方向（从盒指向 c）。
// c 在盒外时是“盒上最近点 → c”的方向；在盒内时取穿透最浅的那个面。
func outsideNormal(c Vec3, b AABB) Vec3 {
	if !b.Contains(c) {
		return safeNormal(c.Sub(ClosestPtPointAABB(c, b)))
	}
	bestAxis, bestSign := 0, 1.0
	bestDist := b.Max.X - c.X
	for i := 0; i < 3; i++ {
		lo := c.At(i) - b.Min.At(i) // 到负向面的距离
		hi := b.Max.At(i) - c.At(i) // 到正向面的距离
		if lo < bestDist {
			bestDist, bestAxis, bestSign = lo, i, -1
		}
		if hi < bestDist {
			bestDist, bestAxis, bestSign = hi, i, 1
		}
	}
	return Vec3{}.WithAt(bestAxis, bestSign)
}
