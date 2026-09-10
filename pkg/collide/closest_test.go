package collide

import "testing"

func vecAlmostEq(a, b Vec3) bool {
	return almostEq(a.X, b.X) && almostEq(a.Y, b.Y) && almostEq(a.Z, b.Z)
}

func TestClosestPtPointPlane(t *testing.T) {
	pl := Plane{N: Vec3{0, 1, 0}, D: 0} // y = 0 平面
	got := ClosestPtPointPlane(Vec3{3, 5, -2}, pl)
	if !vecAlmostEq(got, Vec3{3, 0, -2}) {
		t.Fatalf("closest = %v, want (3,0,-2)", got)
	}
	if d := SignedDistancePointPlane(Vec3{3, 5, -2}, pl); !almostEq(d, 5) {
		t.Fatalf("signed distance = %v, want 5", d)
	}
	// 非单位法向：2y = 0 应与 y = 0 给出同样结果
	pl2 := Plane{N: Vec3{0, 2, 0}, D: 0}
	if got := ClosestPtPointPlane(Vec3{3, 5, -2}, pl2); !vecAlmostEq(got, Vec3{3, 0, -2}) {
		t.Fatalf("closest (scaled normal) = %v, want (3,0,-2)", got)
	}
}

func TestClosestPtPointSegment(t *testing.T) {
	a, b := Vec3{0, 0, 0}, Vec3{4, 0, 0}
	cases := []struct {
		c, want Vec3
	}{
		{Vec3{2, 3, 0}, Vec3{2, 0, 0}},  // 投影在段内
		{Vec3{-1, 0, 0}, Vec3{0, 0, 0}}, // 落在 A 端外
		{Vec3{10, 0, 0}, Vec3{4, 0, 0}}, // 落在 B 端外
	}
	for _, tc := range cases {
		if got := ClosestPtPointSegment(tc.c, a, b); !vecAlmostEq(got, tc.want) {
			t.Fatalf("closest(%v) = %v, want %v", tc.c, got, tc.want)
		}
	}
	// 退化线段
	if got := ClosestPtPointSegment(Vec3{2, 2, 2}, Vec3{1, 1, 1}, Vec3{1, 1, 1}); !vecAlmostEq(got, Vec3{1, 1, 1}) {
		t.Fatalf("degenerate segment closest = %v, want (1,1,1)", got)
	}
}

func TestSqDistPointSegment(t *testing.T) {
	a, b := Vec3{0, 0, 0}, Vec3{4, 0, 0}
	if d := SqDistPointSegment(a, b, Vec3{2, 3, 0}); !almostEq(d, 9) {
		t.Fatalf("inside sqdist = %v, want 9", d)
	}
	if d := SqDistPointSegment(a, b, Vec3{-1, 0, 0}); !almostEq(d, 1) {
		t.Fatalf("left sqdist = %v, want 1", d)
	}
	if d := SqDistPointSegment(a, b, Vec3{10, 0, 0}); !almostEq(d, 36) {
		t.Fatalf("right sqdist = %v, want 36", d)
	}
}

func TestClosestPtPointAABB(t *testing.T) {
	box := AABB{Min: Vec3{0, 0, 0}, Max: Vec3{1, 1, 1}}
	if got := ClosestPtPointAABB(Vec3{0.5, 0.5, 0.5}, box); !vecAlmostEq(got, Vec3{0.5, 0.5, 0.5}) {
		t.Fatalf("inside closest = %v, want self", got)
	}
	if got := ClosestPtPointAABB(Vec3{2, 0.5, -1}, box); !vecAlmostEq(got, Vec3{1, 0.5, 0}) {
		t.Fatalf("outside closest = %v, want (1,0.5,0)", got)
	}
}

func TestSqDistPointAABB(t *testing.T) {
	box := AABB{Min: Vec3{0, 0, 0}, Max: Vec3{1, 1, 1}}
	if d := SqDistPointAABB(Vec3{0.5, 0.5, 0.5}, box); !almostEq(d, 0) {
		t.Fatalf("inside sqdist = %v, want 0", d)
	}
	if d := SqDistPointAABB(Vec3{2, 2, 2}, box); !almostEq(d, 3) {
		t.Fatalf("outside sqdist = %v, want 3", d)
	}
}

func TestClosestPtPointOBB(t *testing.T) {
	box := AABB{Min: Vec3{-1, -1, -1}, Max: Vec3{1, 1, 1}}
	obb := AABBToOBB(box)
	if got := ClosestPtPointOBB(Vec3{3, 0, 0}, obb); !vecAlmostEq(got, Vec3{1, 0, 0}) {
		t.Fatalf("axis-aligned obb closest = %v, want (1,0,0)", got)
	}
	if d := SqDistPointOBB(Vec3{3, 0, 0}, obb); !almostEq(d, 4) {
		t.Fatalf("obb sqdist = %v, want 4", d)
	}
	// 绕 z 轴转 90°：局部 x 轴变成世界 -y，局部 y 轴变成世界 +x
	rot := OBB{
		C: Vec3{0, 0, 0},
		U: [3]Vec3{{0, 1, 0}, {-1, 0, 0}, {0, 0, 1}},
		E: [3]float64{1, 1, 1},
	}
	if got := ClosestPtPointOBB(Vec3{3, 0, 0}, rot); !vecAlmostEq(got, Vec3{1, 0, 0}) {
		t.Fatalf("rotated obb closest = %v, want (1,0,0)", got)
	}
}

func TestClosestPtSegmentSegment(t *testing.T) {
	// 两线段相交，距离 0
	_, _, c1, c2, d2 := ClosestPtSegmentSegment(
		Vec3{0, 0, 0}, Vec3{1, 0, 0},
		Vec3{0.5, -1, 0}, Vec3{0.5, 1, 0},
	)
	if !almostEq(d2, 0) || !vecAlmostEq(c1, Vec3{0.5, 0, 0}) || !vecAlmostEq(c2, Vec3{0.5, 0, 0}) {
		t.Fatalf("crossing: c1=%v c2=%v distSq=%v, want both (0.5,0,0) dist 0", c1, c2, d2)
	}

	// 平行且投影重叠：距离 1
	_, _, p1, p2, pd := ClosestPtSegmentSegment(
		Vec3{0, 0, 0}, Vec3{2, 0, 0},
		Vec3{1, 1, 0}, Vec3{3, 1, 0},
	)
	if !almostEq(pd, 1) {
		t.Fatalf("parallel distSq = %v, want 1 (p1=%v p2=%v)", pd, p1, p2)
	}

	// 异面（平行）：x 方向重叠，y/z 各差 1 => 平方距离 2
	_, _, _, _, sd := ClosestPtSegmentSegment(
		Vec3{0, 0, 0}, Vec3{1, 0, 0},
		Vec3{0, 1, 1}, Vec3{1, 1, 1},
	)
	if !almostEq(sd, 2) {
		t.Fatalf("skew distSq = %v, want 2", sd)
	}
}

func TestClosestPtPointTriangle(t *testing.T) {
	a, b, c := Vec3{0, 0, 0}, Vec3{1, 0, 0}, Vec3{0, 1, 0}
	cases := []struct {
		p, want Vec3
	}{
		{Vec3{0.25, 0.25, 1}, Vec3{0.25, 0.25, 0}}, // 面区域
		{Vec3{2, 0, 0}, Vec3{1, 0, 0}},             // 顶点 B
		{Vec3{-1, -1, 0}, Vec3{0, 0, 0}},           // 顶点 A
		{Vec3{0, 2, 0}, Vec3{0, 1, 0}},             // 顶点 C
	}
	for _, tc := range cases {
		if got := ClosestPtPointTriangle(tc.p, a, b, c); !vecAlmostEq(got, tc.want) {
			t.Fatalf("closest(%v) = %v, want %v", tc.p, got, tc.want)
		}
	}
	if d := SqDistPointTriangle(Vec3{0.25, 0.25, 1}, a, b, c); !almostEq(d, 1) {
		t.Fatalf("triangle sqdist = %v, want 1", d)
	}
}
