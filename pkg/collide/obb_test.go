package collide

import (
	"math"
	"testing"
)

func boxOBB(min, max Vec3) OBB {
	return AABBToOBB(AABB{Min: min, Max: max})
}

func TestTestOBBOBB(t *testing.T) {
	a := boxOBB(Vec3{0, 0, 0}, Vec3{2, 2, 2})

	// 重叠
	if !TestOBBOBB(a, boxOBB(Vec3{1.5, 0, 0}, Vec3{3.5, 2, 2})) {
		t.Fatal("overlapping boxes should intersect")
	}
	// 相切（d == rA+rB）
	if !TestOBBOBB(a, boxOBB(Vec3{2, 0, 0}, Vec3{4, 2, 2})) {
		t.Fatal("boxes touching at a face should intersect")
	}
	// 分离
	if TestOBBOBB(a, boxOBB(Vec3{3, 0, 0}, Vec3{5, 2, 2})) {
		t.Fatal("separated boxes should not intersect")
	}

	// 绕 Y 轴旋转 45°：x 方向半宽变成 (cos45+sin45) ≈ 1.414
	s := math.Sqrt2 / 2
	rot := OBB{
		C: Vec3{0, 0, 0},
		U: [3]Vec3{{X: s, Z: -s}, {Y: 1}, {X: s, Z: s}},
		E: [3]float64{1, 1, 1},
	}
	if TestOBBOBB(rot, boxOBB(Vec3{3, -0.5, -0.5}, Vec3{4, 0.5, 0.5})) {
		t.Fatal("rotated box should be separated when centers are 3.5 apart (reach ≈1.914)")
	}
	// 中心距 1.5 < 1.914 => 相交
	if !TestOBBOBB(rot, boxOBB(Vec3{1.0, -0.5, -0.5}, Vec3{2.0, 0.5, 0.5})) {
		t.Fatal("rotated box should overlap when centers are 1.5 apart")
	}
}

func TestContactOBBOBB(t *testing.T) {
	a := boxOBB(Vec3{0, 0, 0}, Vec3{2, 2, 2})
	b := boxOBB(Vec3{1.5, 0, 0}, Vec3{3.5, 2, 2})
	c, ok := ContactOBBOBB(a, b)
	if !ok {
		t.Fatal("should be in contact")
	}
	if !vecAlmostEq(c.Normal, Vec3{-1, 0, 0}) {
		t.Fatalf("Normal = %v, want (-1,0,0)", c.Normal)
	}
	if !almostEq(c.Depth, 0.5) {
		t.Fatalf("Depth = %v, want 0.5", c.Depth)
	}
	if !vecAlmostEq(c.Point, Vec3{1.75, 1, 1}) {
		t.Fatalf("Point = %v, want (1.75,1,1)", c.Point)
	}

	// 与 AABB 版本交叉验证：轴对齐输入下两者应一致
	ca, _ := ContactAABBAABB(
		AABB{Min: Vec3{0, 0, 0}, Max: Vec3{2, 2, 2}},
		AABB{Min: Vec3{1.5, 0, 0}, Max: Vec3{3.5, 2, 2}},
	)
	if !almostEq(ca.Depth, c.Depth) || !vecAlmostEq(ca.Normal, c.Normal) {
		t.Fatalf("OBB 与 AABB 版本不一致: aabb=%+v obb=%+v", ca, c)
	}

	// 分离
	if _, ok := ContactOBBOBB(a, boxOBB(Vec3{3, 0, 0}, Vec3{5, 2, 2})); ok {
		t.Fatal("separated boxes should not be in contact")
	}
}

func TestClosestPtSegmentOBB(t *testing.T) {
	box := boxOBB(Vec3{-1, -1, -1}, Vec3{1, 1, 1})
	cases := []struct {
		name     string
		a, b     Vec3
		wantDist float64
	}{
		{"侧面外", Vec3{-4, 0, 0}, Vec3{-2, 0, 0}, 1},          // 到 x=-1 面距离 1
		{"正上方", Vec3{0, 5, 0}, Vec3{0, 5, 0.5}, 4},          // 到 y=1 面距离 4
		{"穿过盒", Vec3{-3, 0, 0}, Vec3{3, 0, 0}, 0},           // 穿过 => 距离 0
		{"斜穿", Vec3{-3, -3, 0}, Vec3{3, 3, 0}, 0},           // 过原点 => 0
		{"角外", Vec3{3, 3, 3}, Vec3{5, 5, 5}, math.Sqrt(12)}, // 到角点 (1,1,1)
	}
	for _, tc := range cases {
		_, _, d2 := ClosestPtSegmentOBB(tc.a, tc.b, box)
		if math.Abs(math.Sqrt(d2)-tc.wantDist) > 1e-6 {
			t.Fatalf("%s: 距离 = %v, want %v", tc.name, math.Sqrt(d2), tc.wantDist)
		}
	}

	// 最近点应当落在盒表面、且连线与盒最近点一致
	p, q, _ := ClosestPtSegmentOBB(Vec3{-4, 0, 0}, Vec3{-2, 0, 0}, box)
	if !vecAlmostEq(p, Vec3{-2, 0, 0}) || !vecAlmostEq(q, Vec3{-1, 0, 0}) {
		t.Fatalf("closest pair = (%v, %v), want ((-2,0,0), (-1,0,0))", p, q)
	}
}

func TestContactCapsuleOBB(t *testing.T) {
	box := boxOBB(Vec3{-1, -1, -1}, Vec3{1, 1, 1})

	// 太远：不接触
	far := Capsule{A: Vec3{0, -0.5, 5}, B: Vec3{0, 0.5, 5}, R: 0.5}
	if _, ok := ContactCapsuleOBB(far, box); ok {
		t.Fatal("far capsule should not touch box")
	}

	// 靠近 +z 面：轴距 0.2，半径 0.5 => 深度 0.3
	near := Capsule{A: Vec3{0, -0.5, 1.2}, B: Vec3{0, 0.5, 1.2}, R: 0.5}
	c, ok := ContactCapsuleOBB(near, box)
	if !ok {
		t.Fatal("near capsule should touch box")
	}
	if !vecAlmostEq(c.Normal, Vec3{0, 0, 1}) {
		t.Fatalf("Normal = %v, want (0,0,1)（盒 -> 胶囊）", c.Normal)
	}
	if !almostEq(c.Depth, 0.3) {
		t.Fatalf("Depth = %v, want 0.3", c.Depth)
	}
	if !almostEq(c.Point.Z, 0.85) {
		t.Fatalf("Point = %v, want z=0.85", c.Point)
	}
}

// 武器（细长 OBB）打到身体（胶囊）：这是劈砍的核心查询。
func TestBladeVsBody(t *testing.T) {
	// 刀：沿 +x 伸出，长 2（半长 1），薄
	blade := OBB{
		C: Vec3{0, 1, 0},
		U: [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}},
		E: [3]float64{1, 0.05, 0.12},
	}
	// 身体胶囊：轴在 x=1.2（刀到 x=1 结束），轴距 0.2 < R 0.4 => 命中
	body := Capsule{A: Vec3{1.2, 0, 0}, B: Vec3{1.2, 1.8, 0}, R: 0.4}
	c, ok := ContactCapsuleOBB(body, blade)
	if !ok {
		t.Fatal("blade should reach the body")
	}
	if !almostEq(c.Depth, 0.2) {
		t.Fatalf("Depth = %v, want 0.2", c.Depth)
	}
	// 刀够不到的身体：轴距 2.5 > 0.4 => 不接触
	bodyFar := Capsule{A: Vec3{3.5, 0, 0}, B: Vec3{3.5, 1.8, 0}, R: 0.4}
	if _, ok := ContactCapsuleOBB(bodyFar, blade); ok {
		t.Fatal("blade should not reach a body whose axis is 2.5 from the blade face")
	}
}
