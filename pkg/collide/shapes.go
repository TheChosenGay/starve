package collide

import "starve/pkg/collide/kind"

// 本文件是"按种类双分发"的统一入口：调用方把两个 Shape 丢进来，
// 库内部按 Kind 派发到具体的测试函数。
//
// 未实现的组合一律 panic，不静默返回 false。
// 理由：宽阶段的全部价值是"零漏判"，一个静默的 false 会让这个保证失效，
// 而症状（偶尔打不中）极难定位。宁可开发期就吵起来。

// touchEpsSq 是"相切"判定的平方距离容差（点/线段这类零体积图元用）。
const touchEpsSq = 1e-18

func unsupported(name string, a, b Shape) {
	panic("collide: " + name + " 未支持的图元组合: " + a.Kind().String() + " × " + b.Kind().String())
}

// TestShapes 对两个图元做相交判定（含相切），顺序无关。
//
// 已支持的组合（任意顺序）：
//
//	Sphere   × Sphere / Capsule / Point / Segment / Triangle / AABB / OBB
//	Capsule  × Capsule / Segment / AABB / OBB
//	AABB     × AABB / OBB
//	OBB      × OBB
//	Segment  × Segment
func TestShapes(a, b Shape) bool {
	ak, bk := a.Kind(), b.Kind()
	if ak > bk {
		a, b, ak, bk = b, a, bk, ak
	}
	switch ak {
	case kind.Point:
		p := a.(Vec3)
		switch bk {
		case kind.Segment:
			s := b.(Segment)
			return SqDistPointSegment(s.A, s.B, p) <= touchEpsSq
		case kind.Triangle:
			t := b.(Triangle)
			return SqDistPointTriangle(p, t.V0, t.V1, t.V2) <= touchEpsSq
		case kind.AABB:
			return b.(AABB).Contains(p)
		case kind.OBB:
			return SqDistPointOBB(p, b.(OBB)) <= touchEpsSq
		case kind.Sphere:
			s := b.(Sphere)
			return p.DistanceSq(s.C) <= s.R*s.R
		case kind.Capsule:
			c := b.(Capsule)
			return SqDistPointSegment(c.A, c.B, p) <= c.R*c.R
		}
	case kind.Segment:
		s := a.(Segment)
		switch bk {
		case kind.Segment:
			o := b.(Segment)
			_, _, _, _, d2 := ClosestPtSegmentSegment(s.A, s.B, o.A, o.B)
			return d2 <= touchEpsSq
		case kind.OBB:
			return SqDistSegmentOBB(s.A, s.B, b.(OBB)) <= touchEpsSq
		case kind.Sphere:
			sp := b.(Sphere)
			return SqDistPointSegment(s.A, s.B, sp.C) <= sp.R*sp.R
		case kind.Capsule:
			c := b.(Capsule)
			_, _, _, _, d2 := ClosestPtSegmentSegment(s.A, s.B, c.A, c.B)
			return d2 <= c.R*c.R
		}
	case kind.Triangle:
		if bk == kind.Sphere {
			t := a.(Triangle)
			s := b.(Sphere)
			return TestSphereTriangle(s, t.V0, t.V1, t.V2)
		}
	case kind.AABB:
		box := a.(AABB)
		switch bk {
		case kind.AABB:
			return TestAABBAABB(box, b.(AABB))
		case kind.OBB:
			return TestOBBOBB(AABBToOBB(box), b.(OBB))
		case kind.Sphere:
			return TestSphereAABB(b.(Sphere), box)
		case kind.Capsule:
			c := b.(Capsule)
			return SqDistSegmentOBB(c.A, c.B, AABBToOBB(box)) <= c.R*c.R
		}
	case kind.OBB:
		box := a.(OBB)
		switch bk {
		case kind.OBB:
			return TestOBBOBB(box, b.(OBB))
		case kind.Sphere:
			return TestSphereOBB(b.(Sphere), box)
		case kind.Capsule:
			c := b.(Capsule)
			return SqDistSegmentOBB(c.A, c.B, box) <= c.R*c.R
		}
	case kind.Sphere:
		s := a.(Sphere)
		switch bk {
		case kind.Sphere:
			return TestSphereSphere(s, b.(Sphere))
		case kind.Capsule:
			return TestSphereCapsule(s, b.(Capsule))
		}
	case kind.Capsule:
		if bk == kind.Capsule {
			return TestCapsuleCapsule(a.(Capsule), b.(Capsule))
		}
	}
	unsupported("TestShapes", a, b)
	return false
}

// ContactShapes 求两个图元的接触信息（法向 / 深度 / 接触点），顺序无关。
//
// 约定与各 Contact* 一致：Normal 从第二个参数（b）指向第一个参数（a），
// 即"把 a 沿 +Normal 推开"即可分离。
//
// 已支持的组合：
//
//	Sphere  × Sphere / Capsule / AABB / OBB
//	Capsule × Capsule / AABB / OBB
//	AABB    × AABB / OBB
//	OBB     × OBB
func ContactShapes(a, b Shape) (Contact, bool) {
	// 规范化顺序后递归一次；交换了参数就必须把法向翻回来。
	if a.Kind() > b.Kind() {
		c, ok := ContactShapes(b, a)
		c.Normal = c.Normal.Neg()
		return c, ok
	}
	switch a.Kind() {
	case kind.AABB:
		box := a.(AABB)
		switch b.Kind() {
		case kind.AABB:
			return ContactAABBAABB(box, b.(AABB))
		case kind.OBB:
			c, ok := ContactOBBOBB(b.(OBB), AABBToOBB(box)) // 法向 盒→AABB
			c.Normal = c.Normal.Neg()
			return c, ok
		case kind.Sphere:
			c, ok := ContactSphereAABB(b.(Sphere), box) // 法向 盒→球
			c.Normal = c.Normal.Neg()
			return c, ok
		case kind.Capsule:
			c, ok := ContactCapsuleOBB(b.(Capsule), AABBToOBB(box)) // 法向 盒→胶囊
			c.Normal = c.Normal.Neg()
			return c, ok
		}
	case kind.OBB:
		box := a.(OBB)
		switch b.Kind() {
		case kind.OBB:
			return ContactOBBOBB(box, b.(OBB))
		case kind.Sphere:
			c, ok := ContactSphereOBB(b.(Sphere), box) // 法向 盒→球
			c.Normal = c.Normal.Neg()
			return c, ok
		case kind.Capsule:
			c, ok := ContactCapsuleOBB(b.(Capsule), box) // 法向 盒→胶囊
			c.Normal = c.Normal.Neg()
			return c, ok
		}
	case kind.Sphere:
		s := a.(Sphere)
		switch b.Kind() {
		case kind.Sphere:
			return ContactSphereSphere(s, b.(Sphere))
		case kind.Capsule:
			return ContactSphereCapsule(s, b.(Capsule))
		}
	case kind.Capsule:
		if b.Kind() == kind.Capsule {
			return ContactCapsuleCapsule(a.(Capsule), b.(Capsule))
		}
	}
	unsupported("ContactShapes", a, b)
	return Contact{}, false
}

// IntersectRayShape 求射线与图元的首次相交。
// 目前支持 Sphere / AABB / OBB（其余组合 panic，见文件头说明）。
func IntersectRayShape(o, dir Vec3, maxDist float64, s Shape) Hit {
	switch s.Kind() {
	case kind.Sphere:
		return IntersectRaySphere(o, dir, maxDist, s.(Sphere))
	case kind.AABB:
		return IntersectRayAABB(o, dir, maxDist, s.(AABB))
	case kind.OBB:
		return IntersectRayOBB(o, dir, maxDist, s.(OBB))
	}
	panic("collide: IntersectRayShape 未支持的图元: " + s.Kind().String())
}

// SweepShapes 让移动体 moving 沿 motion 平移，对静止图元 target 求首次接触。
// 目前只实现了 moving 为 Sphere 的组合（球是子弹/投掷物的通用近似）。
func SweepShapes(moving Shape, motion Vec3, target Shape) Hit {
	s, ok := moving.(Sphere)
	if !ok {
		panic("collide: SweepShapes 目前只支持 Sphere 作为移动体，收到 " + moving.Kind().String())
	}
	switch target.Kind() {
	case kind.Sphere:
		return SweepSphereSphere(s, motion, target.(Sphere))
	case kind.AABB:
		return SweepSphereAABB(s, motion, target.(AABB))
	case kind.OBB:
		return SweepSphereOBB(s, motion, target.(OBB))
	case kind.Capsule:
		return SweepSphereCapsule(s, motion, target.(Capsule))
	case kind.Triangle:
		t := target.(Triangle)
		return SweepSphereTriangle(s, motion, t.V0, t.V1, t.V2)
	}
	panic("collide: SweepShapes 未支持的目标图元: " + target.Kind().String())
}
