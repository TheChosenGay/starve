package collide

import (
	"math"
	"testing"
)

func TestBoundsPrimitives(t *testing.T) {
	s := Sphere{C: Vec3{X: 1, Y: 2, Z: 3}, R: 0.5}
	b := s.Bounds()
	if !vecAlmostEq(b.Min, Vec3{X: 0.5, Y: 1.5, Z: 2.5}) || !vecAlmostEq(b.Max, Vec3{X: 1.5, Y: 2.5, Z: 3.5}) {
		t.Fatalf("Sphere.Bounds = %v..%v", b.Min, b.Max)
	}

	c := Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 2, Z: 0}, R: 0.3}
	b = c.Bounds()
	if !vecAlmostEq(b.Min, Vec3{X: -0.3, Y: -0.3, Z: -0.3}) || !vecAlmostEq(b.Max, Vec3{X: 0.3, Y: 2.3, Z: 0.3}) {
		t.Fatalf("Capsule.Bounds = %v..%v", b.Min, b.Max)
	}

	// 轴对齐的 OBB 应该和等价的 AABB 给出同一个盒。
	obox := OBB{C: Vec3{X: 1, Y: 1, Z: 1}, U: [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}}, E: [3]float64{2, 1, 0.5}}
	got, want := obox.Bounds(), (AABB{Min: Vec3{X: -1, Y: 0, Z: 0.5}, Max: Vec3{X: 3, Y: 2, Z: 1.5}})
	if !vecAlmostEq(got.Min, want.Min) || !vecAlmostEq(got.Max, want.Max) {
		t.Fatalf("OBB.Bounds = %v..%v, want %v..%v", got.Min, got.Max, want.Min, want.Max)
	}

	// 旋转 45° 的 OBB：包围盒必须比轴对齐时更胖（√2 倍投影半径）。
	rot := RotateAbout(Vec3{X: 2, Y: 0, Z: 0}, Vec3{}, Vec3{Y: 1}, math.Pi/4)
	obox = OBB{C: Vec3{}, U: [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}}, E: [3]float64{2, 1, 0.5}}
	obox.U[0] = rot.Normalized()
	obox.U[2] = obox.U[0].Cross(obox.U[1])
	got, want = obox.Bounds(), AABB{
		// U0 与 U2 都在 XZ 平面内、与轴成 45°，投影半径 (2+0.5)/√2。
		Min: Vec3{X: -2.5 / math.Sqrt2, Y: -1, Z: -2.5 / math.Sqrt2},
		Max: Vec3{X: 2.5 / math.Sqrt2, Y: 1, Z: 2.5 / math.Sqrt2},
	}
	if !vecAlmostEq(got.Min, want.Min) || !vecAlmostEq(got.Max, want.Max) {
		t.Fatalf("rotated OBB.Bounds = %v..%v, want %v..%v", got.Min, got.Max, want.Min, want.Max)
	}
}

func TestAABBOps(t *testing.T) {
	a := AABB{Min: Vec3{X: 0, Y: 0, Z: 0}, Max: Vec3{X: 1, Y: 1, Z: 1}}
	b := AABB{Min: Vec3{X: 2, Y: -1, Z: 0}, Max: Vec3{X: 3, Y: 0, Z: 1}}
	u := a.Union(b)
	if !vecAlmostEq(u.Min, Vec3{X: 0, Y: -1, Z: 0}) || !vecAlmostEq(u.Max, Vec3{X: 3, Y: 1, Z: 1}) {
		t.Fatalf("Union = %v..%v", u.Min, u.Max)
	}
	if !AABBUnion(a, b).ContainsBox(a) {
		t.Fatal("AABBUnion 应包含 a")
	}
	f := FatAABB(a, 0.25)
	if !vecAlmostEq(f.Min, Vec3{X: -0.25, Y: -0.25, Z: -0.25}) || !vecAlmostEq(f.Max, Vec3{X: 1.25, Y: 1.25, Z: 1.25}) {
		t.Fatalf("FatAABB = %v..%v", f.Min, f.Max)
	}
	if !f.ContainsBox(a) || a.ContainsBox(f) {
		t.Fatal("FatAABB 应严格包含原盒")
	}
	if FatAABB(a, 0) != a {
		t.Fatal("margin 0 应返回原盒")
	}
	sw := SweptAABB(a, Vec3{X: 2, Y: -3, Z: 0})
	if !vecAlmostEq(sw.Min, Vec3{X: 0, Y: -3, Z: 0}) || !vecAlmostEq(sw.Max, Vec3{X: 3, Y: 1, Z: 1}) {
		t.Fatalf("SweptAABB = %v..%v", sw.Min, sw.Max)
	}
}

func TestRotateAbout(t *testing.T) {
	// 绕 Y 轴转 90°：+X → -Z（右手定则，Y 向上）。
	got := RotateAbout(Vec3{X: 1}, Vec3{}, Vec3{Y: 1}, math.Pi/2)
	if !vecAlmostEq(got, Vec3{Z: -1}) {
		t.Fatalf("RotateAbout(+X, +Y, 90°) = %v, want (0,0,-1)", got)
	}
	// 绕任意轴转一整圈回到原点。
	axes := []Vec3{{X: 1}, {Y: 1}, {Z: 1}, {X: 1, Y: 2, Z: -0.5}}
	p := Vec3{X: 1.5, Y: -0.5, Z: 2}
	for _, ax := range axes {
		if got := RotateAbout(p, Vec3{X: 9, Y: 1, Z: -3}, ax, 2*math.Pi); !vecAlmostEq(got, p) {
			t.Fatalf("整圈旋转应回到起点（axis=%v）：got %v", ax, got)
		}
	}
	// 旋转保距：到轴心的距离不变。
	pivot := Vec3{X: 0.5, Y: 0, Z: -1}
	for angle := -3.0; angle <= 3.0; angle += 0.25 {
		got := RotateAbout(p, pivot, Vec3{Y: 1}, angle)
		if math.Abs(got.Distance(pivot)-p.Distance(pivot)) > 1e-12 {
			t.Fatalf("旋转改变到轴心的距离：angle=%v", angle)
		}
	}
}
