package collide

// ClosestSurfacePoint 返回图元表面上离 p 最近的点。
//
// 与 ClosestPtPoint* 系列的区别：对带半径的图元（Sphere / Capsule）返回**真实表面**上的点，
// 而不是中心或轴线上的点。命中标记、贴花、特效挂点用它。
func ClosestSurfacePoint(p Vec3, s Solid) Vec3 {
	switch v := s.(type) {
	case Vec3:
		return v
	case Segment:
		return ClosestPtPointSegment(p, v.A, v.B)
	case Triangle:
		return ClosestPtPointTriangle(p, v.V0, v.V1, v.V2)
	case AABB:
		return ClosestPtPointAABB(p, v)
	case OBB:
		return ClosestPtPointOBB(p, v)
	case Sphere:
		return v.C.Add(safeNormal(p.Sub(v.C)).Scale(v.R))
	case Capsule:
		q := ClosestPtPointSegment(p, v.A, v.B)
		return q.Add(safeNormal(p.Sub(q)).Scale(v.R))
	}
	panic("collide: ClosestSurfacePoint 未支持的图元")
}

// EachProbe 遍历图元的代表点，每个点带自己的半径（球/胶囊是真实半径，零体积图元是 0）。
//
// 扇形这类**体积查询**用它做近似相交判定：任意一个探针落在查询体积里就算相交。
// 只探一个点是不够的——竖直劈砍打高个子时，目标顶部/底部都可能先进刀路，
// 而"离扇心最近的那个点"往往固定在胸口高度，看不出竖直方向上的差异。
//
// fn 返回 false 表示提前结束（已经判定相交时就不必再探）。
func EachProbe(s Solid, fn func(p Vec3, r float64) bool) {
	switch v := s.(type) {
	case Vec3:
		fn(v, 0)
	case Segment:
		for i := 0; i <= 2; i++ {
			if !fn(v.A.Lerp(v.B, float64(i)/2), 0) {
				return
			}
		}
	case Triangle:
		centroid := v.V0.Add(v.V1).Add(v.V2).Scale(1.0 / 3)
		for _, p := range [4]Vec3{v.V0, v.V1, v.V2, centroid} {
			if !fn(p, 0) {
				return
			}
		}
	case Sphere:
		fn(v.C, v.R)
	case Capsule:
		const n = 4
		for i := 0; i <= n; i++ {
			if !fn(v.A.Lerp(v.B, float64(i)/n), v.R) {
				return
			}
		}
	case AABB:
		boxProbes(v.Center(), [3]Vec3{
			{X: v.Extents().X}, {Y: v.Extents().Y}, {Z: v.Extents().Z},
		}, fn)
	case OBB:
		boxProbes(v.C, [3]Vec3{
			v.U[0].Scale(v.E[0]), v.U[1].Scale(v.E[1]), v.U[2].Scale(v.E[2]),
		}, fn)
	default:
		panic("collide: EachProbe 未支持的图元")
	}
}

// boxProbes 用「中心 + 8 个角点」作为盒的探针。
func boxProbes(c Vec3, e [3]Vec3, fn func(p Vec3, r float64) bool) {
	if !fn(c, 0) {
		return
	}
	for i := 0; i < 8; i++ {
		p := c
		for a := 0; a < 3; a++ {
			if i&(1<<a) != 0 {
				p = p.Add(e[a])
			} else {
				p = p.Sub(e[a])
			}
		}
		if !fn(p, 0) {
			return
		}
	}
}
