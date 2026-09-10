package collide

// TestSphereSphere 判断两球是否相交（含相切）。
func TestSphereSphere(a, b Sphere) bool {
	r := a.R + b.R
	return a.C.DistanceSq(b.C) <= r*r
}

// TestSphereAABB 判断球与 AABB 是否相交（含相切）。
func TestSphereAABB(s Sphere, b AABB) bool {
	return SqDistPointAABB(s.C, b) <= s.R*s.R
}

// TestSphereOBB 判断球与 OBB 是否相交（含相切）。
func TestSphereOBB(s Sphere, b OBB) bool {
	return SqDistPointOBB(s.C, b) <= s.R*s.R
}

// TestSphereTriangle 判断球与三角形是否相交（含相切）。
func TestSphereTriangle(s Sphere, a, b, c Vec3) bool {
	return SqDistPointTriangle(s.C, a, b, c) <= s.R*s.R
}

// TestSphereCapsule 判断球与胶囊体是否相交（含相切）。
func TestSphereCapsule(s Sphere, cap Capsule) bool {
	r := s.R + cap.R
	return SqDistPointSegment(cap.A, cap.B, s.C) <= r*r
}

// TestCapsuleCapsule 判断两胶囊体是否相交（含相切）。
// 常用于角色身体之间的判定。
func TestCapsuleCapsule(a, b Capsule) bool {
	_, _, _, _, distSq := ClosestPtSegmentSegment(a.A, a.B, b.A, b.B)
	r := a.R + b.R
	return distSq <= r*r
}

// TestAABBAABB 判断两 AABB 是否相交（含相切）。
func TestAABBAABB(a, b AABB) bool {
	return a.Min.X <= b.Max.X && a.Max.X >= b.Min.X &&
		a.Min.Y <= b.Max.Y && a.Max.Y >= b.Min.Y &&
		a.Min.Z <= b.Max.Z && a.Max.Z >= b.Min.Z
}
