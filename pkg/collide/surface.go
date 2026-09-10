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
