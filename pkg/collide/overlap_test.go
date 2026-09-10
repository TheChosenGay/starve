package collide

import "testing"

func TestOverlapSphereSphere(t *testing.T) {
	a := Sphere{C: Vec3{X: 0, Y: 0, Z: 0}, R: 1}
	if !TestSphereSphere(a, Sphere{C: Vec3{X: 2, Y: 0, Z: 0}, R: 1}) {
		t.Fatal("touching spheres should intersect")
	}
	if TestSphereSphere(a, Sphere{C: Vec3{X: 2.1, Y: 0, Z: 0}, R: 1}) {
		t.Fatal("separated spheres should not intersect")
	}
}

func TestOverlapSphereAABB(t *testing.T) {
	box := AABB{Min: Vec3{X: 0, Y: 0, Z: 0}, Max: Vec3{X: 1, Y: 1, Z: 1}}
	if !TestSphereAABB(Sphere{C: Vec3{X: 2, Y: 0.5, Z: 0.5}, R: 1.1}, box) {
		t.Fatal("sphere near box face should intersect")
	}
	if TestSphereAABB(Sphere{C: Vec3{X: 2, Y: 0.5, Z: 0.5}, R: 0.9}, box) {
		t.Fatal("sphere too far from box should not intersect")
	}
}

func TestOverlapSphereOBB(t *testing.T) {
	obb := AABBToOBB(AABB{Min: Vec3{X: -1, Y: -1, Z: -1}, Max: Vec3{X: 1, Y: 1, Z: 1}})
	if !TestSphereOBB(Sphere{C: Vec3{X: 2, Y: 0, Z: 0}, R: 1.1}, obb) {
		t.Fatal("sphere near obb should intersect")
	}
	if TestSphereOBB(Sphere{C: Vec3{X: 2, Y: 0, Z: 0}, R: 0.9}, obb) {
		t.Fatal("sphere away from obb should not intersect")
	}
}

func TestOverlapSphereTriangle(t *testing.T) {
	a, b, c := Vec3{X: 0, Y: 0, Z: 0}, Vec3{X: 2, Y: 0, Z: 0}, Vec3{X: 0, Y: 2, Z: 0}
	if !TestSphereTriangle(Sphere{C: Vec3{X: 0.5, Y: 0.5, Z: 1}, R: 1.1}, a, b, c) {
		t.Fatal("sphere above triangle should intersect")
	}
	if TestSphereTriangle(Sphere{C: Vec3{X: 0.5, Y: 0.5, Z: 1}, R: 0.9}, a, b, c) {
		t.Fatal("sphere above triangle but too small should not intersect")
	}
}

func TestOverlapSphereCapsule(t *testing.T) {
	cap := Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 1, Z: 0}, R: 0.5}
	if !TestSphereCapsule(Sphere{C: Vec3{X: 1, Y: 0.5, Z: 0}, R: 0.6}, cap) {
		t.Fatal("sphere near capsule should intersect")
	}
	if TestSphereCapsule(Sphere{C: Vec3{X: 1, Y: 0.5, Z: 0}, R: 0.4}, cap) {
		t.Fatal("sphere away from capsule should not intersect")
	}
}

func TestOverlapCapsuleCapsule(t *testing.T) {
	a := Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 1, Z: 0}, R: 0.5}
	b := Capsule{A: Vec3{X: 1, Y: 0, Z: 0}, B: Vec3{X: 1, Y: 1, Z: 0}, R: 0.6}
	if !TestCapsuleCapsule(a, b) {
		t.Fatal("capsules within combined radius should intersect")
	}
	c := Capsule{A: Vec3{X: 1, Y: 0, Z: 0}, B: Vec3{X: 1, Y: 1, Z: 0}, R: 0.4}
	if TestCapsuleCapsule(a, c) {
		t.Fatal("capsules beyond combined radius should not intersect")
	}
}

func TestOverlapAABBAABB(t *testing.T) {
	a := AABB{Min: Vec3{X: 0, Y: 0, Z: 0}, Max: Vec3{X: 1, Y: 1, Z: 1}}
	if !TestAABBAABB(a, AABB{Min: Vec3{X: 1, Y: 1, Z: 1}, Max: Vec3{X: 2, Y: 2, Z: 2}}) {
		t.Fatal("boxes touching at corner should intersect")
	}
	if TestAABBAABB(a, AABB{Min: Vec3{X: 1.1, Y: 0, Z: 0}, Max: Vec3{X: 2, Y: 1, Z: 1}}) {
		t.Fatal("separated boxes should not intersect")
	}
}
