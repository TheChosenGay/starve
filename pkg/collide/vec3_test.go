package collide

import (
	"math"
	"testing"
)

func almostEq(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestVec3DotCross(t *testing.T) {
	a := Vec3{1, 2, 3}
	b := Vec3{4, 5, 6}
	if got := a.Dot(b); !almostEq(got, 32) {
		t.Fatalf("Dot = %v, want 32", got)
	}
	c := a.Cross(b)
	if !almostEq(c.X, -3) || !almostEq(c.Y, 6) || !almostEq(c.Z, -3) {
		t.Fatalf("Cross = %v, want (-3,6,-3)", c)
	}
}

func TestVec3Normalize(t *testing.T) {
	n := (Vec3{3, 4, 0}).Normalized()
	if !almostEq(n.Len(), 1) {
		t.Fatalf("normalized length = %v, want 1", n.Len())
	}
	if !almostEq(n.X, 0.6) || !almostEq(n.Y, 0.8) {
		t.Fatalf("normalized = %v, want (0.6,0.8,0)", n)
	}
	if z := (Vec3{}).Normalized(); z != (Vec3{}) {
		t.Fatalf("zero vector normalized = %v, want zero", z)
	}
}

func TestVec3Distance(t *testing.T) {
	if d := (Vec3{0, 0, 0}).Distance(Vec3{1, 2, 2}); !almostEq(d, 3) {
		t.Fatalf("Distance = %v, want 3", d)
	}
	if d := (Vec3{0, 0, 0}).DistanceSq(Vec3{1, 2, 2}); !almostEq(d, 9) {
		t.Fatalf("DistanceSq = %v, want 9", d)
	}
}
