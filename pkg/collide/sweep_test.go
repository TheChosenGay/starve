package collide

import (
	"math"
	"testing"
)

func TestSweepSphereSphere(t *testing.T) {
	s := Sphere{C: Vec3{X: 0, Y: 0, Z: 0}, R: 0.5}
	b := Sphere{C: Vec3{X: 5, Y: 0, Z: 0}, R: 1}
	h := SweepSphereSphere(s, Vec3{X: 10, Y: 0, Z: 0}, b)
	if !h.Hit {
		t.Fatal("should hit")
	}
	// 球心走到 x = 5 - 1.5 = 3.5 时接触 => t = 0.35
	if !almostEq(h.T, 0.35) {
		t.Fatalf("T = %v, want 0.35", h.T)
	}
	if !vecAlmostEq(h.Point, Vec3{X: 4, Y: 0, Z: 0}) {
		t.Fatalf("Point = %v, want (4,0,0)", h.Point)
	}
	if !vecAlmostEq(h.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", h.Normal)
	}
}

// 关键用例：高速球穿过薄盒，离散测试会漏（隧穿），扫掠必须命中。
func TestSweepSphereAABBTunneling(t *testing.T) {
	box := AABB{Min: Vec3{X: 4.9, Y: 0, Z: -1}, Max: Vec3{X: 5.1, Y: 2, Z: 1}}
	s := Sphere{C: Vec3{X: 0, Y: 1, Z: 0}, R: 0.5}
	motion := Vec3{X: 10, Y: 0, Z: 0}

	// 起点、终点分别测试都“不重叠”
	end := Sphere{C: s.C.Add(motion), R: s.R}
	if TestSphereAABB(s, box) || TestSphereAABB(end, box) {
		t.Fatal("test premise broken: start/end should not overlap")
	}
	if SqDistPointAABB(s.C, box) <= s.R*s.R || SqDistPointAABB(end.C, box) <= s.R*s.R {
		t.Fatal("test premise broken: both endpoints farther than radius")
	}

	h := SweepSphereAABB(s, motion, box)
	if !h.Hit {
		t.Fatal("sweep must catch the tunneled hit")
	}
	// 球心 x = 4.9 - 0.5 = 4.4 时接触 => t = 0.44
	if !almostEq(h.T, 0.44) {
		t.Fatalf("T = %v, want 0.44", h.T)
	}
	if !vecAlmostEq(h.Point, Vec3{X: 4.9, Y: 1, Z: 0}) {
		t.Fatalf("Point = %v, want (4.9,1,0)", h.Point)
	}
	if !vecAlmostEq(h.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", h.Normal)
	}
}

func TestSweepSphereAABBNoHit(t *testing.T) {
	box := AABB{Min: Vec3{X: 4.9, Y: 0, Z: -1}, Max: Vec3{X: 5.1, Y: 2, Z: 1}}
	s := Sphere{C: Vec3{X: 0, Y: 5, Z: 0}, R: 0.5} // 高于盒子，扫过去碰不到
	if h := SweepSphereAABB(s, Vec3{X: 10, Y: 0, Z: 0}, box); h.Hit {
		t.Fatalf("should not hit, got %+v", h)
	}
}

func TestSweepFirstSphereAABB(t *testing.T) {
	boxes := []AABB{
		{Min: Vec3{X: 3, Y: 0, Z: -1}, Max: Vec3{X: 3.2, Y: 2, Z: 1}},
		{Min: Vec3{X: 5, Y: 0, Z: -1}, Max: Vec3{X: 5.2, Y: 2, Z: 1}},
		{Min: Vec3{X: 7, Y: 0, Z: -1}, Max: Vec3{X: 7.2, Y: 2, Z: 1}},
	}
	s := Sphere{C: Vec3{X: 0, Y: 1, Z: 0}, R: 0.5}
	idx, h, ok := SweepFirstSphereAABB(s, Vec3{X: 10, Y: 0, Z: 0}, boxes)
	if !ok {
		t.Fatal("should hit something")
	}
	if idx != 0 {
		t.Fatalf("idx = %d, want 0（最先撞到最近的那个）", idx)
	}
	if !almostEq(h.T, 0.25) { // (3 - 0.5) / 10
		t.Fatalf("T = %v, want 0.25", h.T)
	}
}

// 球沿运动扫掠，射程不足时不应命中远盒。
func TestSweepRespectsRange(t *testing.T) {
	box := AABB{Min: Vec3{X: 4.9, Y: 0, Z: -1}, Max: Vec3{X: 5.1, Y: 2, Z: 1}}
	s := Sphere{C: Vec3{X: 0, Y: 1, Z: 0}, R: 0.5}
	if h := SweepSphereAABB(s, Vec3{X: 2, Y: 0, Z: 0}, box); h.Hit { // 只走 2，够不到 x=4.4
		t.Fatalf("range too short, should miss, got %+v", h)
	}
	if h := SweepSphereAABB(s, Vec3{X: 5, Y: 0, Z: 0}, box); !h.Hit { // 走 5，够得到
		t.Fatal("range sufficient, should hit")
	}
}

// 无限投射（射线）用 math.Inf，与有限扫掠共用同一套语义。
func TestCastToInfinity(t *testing.T) {
	s := Sphere{C: Vec3{X: 1000, Y: 0, Z: 0}, R: 1}
	if h := IntersectRaySphere(Vec3{X: 0, Y: 0, Z: 0}, Vec3{X: 1, Y: 0, Z: 0}, math.Inf(1), s); !h.Hit {
		t.Fatal("infinite ray should reach far target")
	}
	if h := IntersectRaySphere(Vec3{X: 0, Y: 0, Z: 0}, Vec3{X: 1, Y: 0, Z: 0}, 100, s); h.Hit {
		t.Fatal("bounded ray should not reach target at 1000")
	}
}

// 子弹打人：球沿运动扫掠，首次接触胶囊（= 子弹打身体）。
func TestSweepSphereCapsule(t *testing.T) {
	body := Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 2, Z: 0}, R: 0.4}
	bullet := Sphere{C: Vec3{X: -5, Y: 1, Z: 0}, R: 0.25}
	motion := Vec3{X: 10, Y: 0, Z: 0}

	// 离散的起点/终点都不重叠
	if TestSphereCapsule(bullet, body) {
		t.Fatal("premise broken: start should not overlap")
	}
	if TestSphereCapsule(Sphere{C: bullet.C.Add(motion), R: bullet.R}, body) {
		t.Fatal("premise broken: end should not overlap")
	}

	h := SweepSphereCapsule(bullet, motion, body)
	if !h.Hit {
		t.Fatal("bullet should hit the capsule")
	}
	// 球心走到 x = -(0.25+0.4) = -0.65 时接触 => t = 0.435
	if !almostEq(h.T, 0.435) {
		t.Fatalf("T = %v, want 0.435", h.T)
	}
	if !almostEq(h.Dist, 4.35) {
		t.Fatalf("Dist = %v, want 4.35", h.Dist)
	}
	if !vecAlmostEq(h.Point, Vec3{X: -0.4, Y: 1, Z: 0}) {
		t.Fatalf("Point = %v, want (-0.4,1,0)", h.Point)
	}
	if !vecAlmostEq(h.Normal, Vec3{X: -1, Y: 0, Z: 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", h.Normal)
	}
}

// 子弹从胶囊上方掠过 -> 不命中。
func TestSweepSphereCapsuleMiss(t *testing.T) {
	body := Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 2, Z: 0}, R: 0.4}
	bullet := Sphere{C: Vec3{X: -5, Y: 3, Z: 0}, R: 0.25} // 高于胶囊顶端
	if h := SweepSphereCapsule(bullet, Vec3{X: 10, Y: 0, Z: 0}, body); h.Hit {
		t.Fatalf("should miss, got %+v", h)
	}
}
