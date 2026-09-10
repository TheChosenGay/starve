package collide

import "testing"

func TestContactSphereSphere(t *testing.T) {
	a := Sphere{C: Vec3{X: 0, Y: 0, Z: 0}, R: 1}
	b := Sphere{C: Vec3{X: 1.5, Y: 0, Z: 0}, R: 1}
	c, ok := ContactSphereSphere(a, b)
	if !ok {
		t.Fatal("should be in contact")
	}
	if !vecAlmostEq(c.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)（B -> A）", c.Normal)
	}
	if !almostEq(c.Depth, 0.5) {
		t.Fatalf("Depth = %v, want 0.5", c.Depth)
	}
	if !vecAlmostEq(c.Point, Vec3{X: 0.75, Y: 0, Z: 0}) {
		t.Fatalf("Point = %v, want (0.75,0,0)", c.Point)
	}
	if _, ok := ContactSphereSphere(a, Sphere{C: Vec3{X: 3, Y: 0, Z: 0}, R: 1}); ok {
		t.Fatal("separated spheres should not be in contact")
	}
}

func TestContactSphereAABB(t *testing.T) {
	box := AABB{Min: Vec3{X: 0, Y: 0, Z: 0}, Max: Vec3{X: 1, Y: 1, Z: 1}}
	c, ok := ContactSphereAABB(Sphere{C: Vec3{X: 2, Y: 0.5, Z: 0.5}, R: 1.1}, box)
	if !ok {
		t.Fatal("should be in contact")
	}
	if !vecAlmostEq(c.Normal, Vec3{X: 1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (1,0,0)（盒 -> 球心）", c.Normal)
	}
	if !almostEq(c.Depth, 0.1) {
		t.Fatalf("Depth = %v, want 0.1", c.Depth)
	}
	if !vecAlmostEq(c.Point, Vec3{X: 1, Y: 0.5, Z: 0.5}) {
		t.Fatalf("Point = %v, want (1,0.5,0.5)", c.Point)
	}
}

func TestContactSphereAABBInside(t *testing.T) {
	box := AABB{Min: Vec3{X: 0, Y: 0, Z: 0}, Max: Vec3{X: 2, Y: 2, Z: 2}}
	c, ok := ContactSphereAABB(Sphere{C: Vec3{X: 1, Y: 1.8, Z: 1}, R: 0.3}, box)
	if !ok {
		t.Fatal("sphere inside box should be in contact")
	}
	if !vecAlmostEq(c.Normal, Vec3{X: 0, Y: 1, Z: 0}) {
		t.Fatalf("Normal = %v, want (0,1,0)（离 +Y 面最近）", c.Normal)
	}
	if !almostEq(c.Depth, 0.5) {
		t.Fatalf("Depth = %v, want 0.5 (0.3 + 0.2)", c.Depth)
	}
	if !almostEq(c.Point.Y, 2) {
		t.Fatalf("Point = %v, want y=2（接触点在盒面上）", c.Point)
	}
}

func TestContactCapsuleCapsule(t *testing.T) {
	a := Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 2, Z: 0}, R: 0.35}
	b := Capsule{A: Vec3{X: 0.7, Y: 0, Z: 0}, B: Vec3{X: 0.7, Y: 2, Z: 0}, R: 0.4}
	c, ok := ContactCapsuleCapsule(a, b)
	if !ok {
		t.Fatal("axis distance 0.7 <= sum radii 0.75, should contact")
	}
	if !vecAlmostEq(c.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", c.Normal)
	}
	if !almostEq(c.Depth, 0.05) {
		t.Fatalf("Depth = %v, want 0.05", c.Depth)
	}
	far := Capsule{A: Vec3{X: 0.8, Y: 0, Z: 0}, B: Vec3{X: 0.8, Y: 2, Z: 0}, R: 0.4}
	if _, ok := ContactCapsuleCapsule(a, far); ok {
		t.Fatal("axis distance 0.8 > 0.75, should not contact")
	}
}

func TestContactSphereCapsule(t *testing.T) {
	cap := Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 2, Z: 0}, R: 0.4}
	c, ok := ContactSphereCapsule(Sphere{C: Vec3{X: 0.8, Y: 1, Z: 0}, R: 0.5}, cap)
	if !ok {
		t.Fatal("0.8 <= 0.9 should contact")
	}
	if !vecAlmostEq(c.Normal, Vec3{X: 1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (1,0,0)", c.Normal)
	}
	if !almostEq(c.Depth, 0.1) {
		t.Fatalf("Depth = %v, want 0.1", c.Depth)
	}
}

func TestContactAABBAABB(t *testing.T) {
	a := AABB{Min: Vec3{X: 0, Y: 0, Z: 0}, Max: Vec3{X: 2, Y: 2, Z: 2}}
	b := AABB{Min: Vec3{X: 1.5, Y: 0, Z: 0}, Max: Vec3{X: 3.5, Y: 2, Z: 2}}
	c, ok := ContactAABBAABB(a, b)
	if !ok {
		t.Fatal("overlapping boxes should contact")
	}
	if !vecAlmostEq(c.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)（a 在 b 的 -X 侧）", c.Normal)
	}
	if !almostEq(c.Depth, 0.5) {
		t.Fatalf("Depth = %v, want 0.5", c.Depth)
	}
	if !vecAlmostEq(c.Point, Vec3{X: 1.75, Y: 1, Z: 1}) {
		t.Fatalf("Point = %v, want (1.75,1,1)", c.Point)
	}
	if _, ok := ContactAABBAABB(a, AABB{Min: Vec3{X: 3, Y: 0, Z: 0}, Max: Vec3{X: 4, Y: 1, Z: 1}}); ok {
		t.Fatal("separated boxes should not contact")
	}
}
