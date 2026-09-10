package collide

import (
	"math"
	"testing"
)

func TestIntersectRaySphereHit(t *testing.T) {
	s := Sphere{C: Vec3{5, 0, 0}, R: 1}
	h := IntersectRaySphere(Vec3{0, 0, 0}, Vec3{1, 0, 0}, math.Inf(1), s)
	if !h.Hit {
		t.Fatal("ray should hit sphere")
	}
	if !almostEq(h.Dist, 4) {
		t.Fatalf("Dist = %v, want 4", h.Dist)
	}
	if !vecAlmostEq(h.Point, Vec3{4, 0, 0}) {
		t.Fatalf("Point = %v, want (4,0,0)", h.Point)
	}
	if !vecAlmostEq(h.Normal, Vec3{-1, 0, 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", h.Normal)
	}
}

func TestIntersectRaySphereMiss(t *testing.T) {
	s := Sphere{C: Vec3{5, 3, 0}, R: 1}
	if h := IntersectRaySphere(Vec3{0, 0, 0}, Vec3{1, 0, 0}, math.Inf(1), s); h.Hit {
		t.Fatalf("ray should miss, got %+v", h)
	}
}

func TestIntersectRaySphereFromInside(t *testing.T) {
	s := Sphere{C: Vec3{5, 0, 0}, R: 2}
	h := IntersectRaySphere(Vec3{5, 0, 0}, Vec3{1, 0, 0}, math.Inf(1), s)
	if !h.Hit || !almostEq(h.Dist, 0) {
		t.Fatalf("ray from inside should hit at distance 0, got %+v", h)
	}
	if !vecAlmostEq(h.Normal, Vec3{-1, 0, 0}) {
		t.Fatalf("inside normal = %v, want (-1,0,0) (指向球内)", h.Normal)
	}
}

func TestIntersectRayAABB(t *testing.T) {
	box := AABB{Min: Vec3{4, 0, -1}, Max: Vec3{6, 2, 1}}
	h := IntersectRayAABB(Vec3{0, 1, 0}, Vec3{1, 0, 0}, 10, box)
	if !h.Hit {
		t.Fatal("ray should hit box")
	}
	if !almostEq(h.Dist, 4) {
		t.Fatalf("Dist = %v, want 4", h.Dist)
	}
	if !vecAlmostEq(h.Point, Vec3{4, 1, 0}) {
		t.Fatalf("Point = %v, want (4,1,0)", h.Point)
	}
	if !vecAlmostEq(h.Normal, Vec3{-1, 0, 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", h.Normal)
	}
	// 射程不够
	if h := IntersectRayAABB(Vec3{0, 1, 0}, Vec3{1, 0, 0}, 3, box); h.Hit {
		t.Fatal("maxDist=3 should not reach box at x=4")
	}
	// 平行且在 slab 外
	if h := IntersectRayAABB(Vec3{0, 3, 0}, Vec3{1, 0, 0}, 10, box); h.Hit {
		t.Fatal("ray above box should miss")
	}
}
