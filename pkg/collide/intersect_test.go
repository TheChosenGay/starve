package collide

import (
	"math"
	"testing"
)

func TestIntersectRaySphereHit(t *testing.T) {
	s := Sphere{C: Vec3{X: 5, Y: 0, Z: 0}, R: 1}
	h := IntersectRaySphere(Vec3{X: 0, Y: 0, Z: 0}, Vec3{X: 1, Y: 0, Z: 0}, math.Inf(1), s)
	if !h.Hit {
		t.Fatal("ray should hit sphere")
	}
	if !almostEq(h.Dist, 4) {
		t.Fatalf("Dist = %v, want 4", h.Dist)
	}
	if !vecAlmostEq(h.Point, Vec3{X: 4, Y: 0, Z: 0}) {
		t.Fatalf("Point = %v, want (4,0,0)", h.Point)
	}
	if !vecAlmostEq(h.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", h.Normal)
	}
}

func TestIntersectRaySphereMiss(t *testing.T) {
	s := Sphere{C: Vec3{X: 5, Y: 3, Z: 0}, R: 1}
	if h := IntersectRaySphere(Vec3{X: 0, Y: 0, Z: 0}, Vec3{X: 1, Y: 0, Z: 0}, math.Inf(1), s); h.Hit {
		t.Fatalf("ray should miss, got %+v", h)
	}
}

func TestIntersectRaySphereFromInside(t *testing.T) {
	s := Sphere{C: Vec3{X: 5, Y: 0, Z: 0}, R: 2}
	h := IntersectRaySphere(Vec3{X: 5, Y: 0, Z: 0}, Vec3{X: 1, Y: 0, Z: 0}, math.Inf(1), s)
	if !h.Hit || !almostEq(h.Dist, 0) {
		t.Fatalf("ray from inside should hit at distance 0, got %+v", h)
	}
	if !vecAlmostEq(h.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("inside normal = %v, want (-1,0,0) (指向球内)", h.Normal)
	}
}

func TestIntersectRayAABB(t *testing.T) {
	box := AABB{Min: Vec3{X: 4, Y: 0, Z: -1}, Max: Vec3{X: 6, Y: 2, Z: 1}}
	h := IntersectRayAABB(Vec3{X: 0, Y: 1, Z: 0}, Vec3{X: 1, Y: 0, Z: 0}, 10, box)
	if !h.Hit {
		t.Fatal("ray should hit box")
	}
	if !almostEq(h.Dist, 4) {
		t.Fatalf("Dist = %v, want 4", h.Dist)
	}
	if !vecAlmostEq(h.Point, Vec3{X: 4, Y: 1, Z: 0}) {
		t.Fatalf("Point = %v, want (4,1,0)", h.Point)
	}
	if !vecAlmostEq(h.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", h.Normal)
	}
	// 射程不够
	if h := IntersectRayAABB(Vec3{X: 0, Y: 1, Z: 0}, Vec3{X: 1, Y: 0, Z: 0}, 3, box); h.Hit {
		t.Fatal("maxDist=3 should not reach box at x=4")
	}
	// 平行且在 slab 外
	if h := IntersectRayAABB(Vec3{X: 0, Y: 3, Z: 0}, Vec3{X: 1, Y: 0, Z: 0}, 10, box); h.Hit {
		t.Fatal("ray above box should miss")
	}
}
