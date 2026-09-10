package primitive

import (
	"math"
	"testing"

	"starve/pkg/collide/kind"
)

// 编译期断言：哪些图元有体积（Solid）、哪些只是 Shape。
var (
	_ Solid = Vec3{}
	_ Solid = Segment{}
	_ Solid = Triangle{}
	_ Solid = AABB{}
	_ Solid = OBB{}
	_ Solid = Sphere{}
	_ Solid = Capsule{}

	_ Shape = Ray{}
	_ Shape = Line{}
	_ Shape = Plane{}
)

// TestKindStable：类型标识必须与图元一一对应（宽阶段的索引与分发都依赖它）。
func TestKindStable(t *testing.T) {
	cases := []struct {
		s    Shape
		want kind.Kind
	}{
		{Vec3{}, kind.Point},
		{Segment{}, kind.Segment},
		{Line{}, kind.Line},
		{Ray{}, kind.Ray},
		{Plane{}, kind.Plane},
		{Triangle{}, kind.Triangle},
		{AABB{}, kind.AABB},
		{OBB{}, kind.OBB},
		{Sphere{}, kind.Sphere},
		{Capsule{}, kind.Capsule},
	}
	for _, c := range cases {
		if got := c.s.Kind(); got != c.want {
			t.Errorf("%T.Kind() = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestBoundsAndBoxOps(t *testing.T) {
	s := Sphere{C: Vec3{X: 1, Y: 2, Z: 3}, R: 0.5}
	if got := s.Bounds(); got.Min != (Vec3{X: 0.5, Y: 1.5, Z: 2.5}) || got.Max != (Vec3{X: 1.5, Y: 2.5, Z: 3.5}) {
		t.Fatalf("Sphere.Bounds = %v..%v", got.Min, got.Max)
	}
	c := Capsule{A: Vec3{}, B: Vec3{Y: 2}, R: 0.3}
	if got := c.Bounds(); got.Min != (Vec3{X: -0.3, Y: -0.3, Z: -0.3}) {
		t.Fatalf("Capsule.Bounds.Min = %v", got.Min)
	}
	// OBB 旋转 45° 后包围盒按投影半径放大
	obb := OBB{C: Vec3{}, U: [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}}, E: [3]float64{2, 1, 0.5}}
	obb.U[0] = Vec3{X: math.Sqrt2 / 2, Z: -math.Sqrt2 / 2}
	obb.U[2] = Vec3{X: math.Sqrt2 / 2, Z: math.Sqrt2 / 2}
	got := obb.Bounds()
	want := 2.5 / math.Sqrt2
	if math.Abs(got.Max.X-want) > 1e-12 || math.Abs(got.Max.Z-want) > 1e-12 {
		t.Fatalf("旋转 OBB 的包围盒 = %v..%v, want ±%v", got.Min, got.Max, want)
	}

	a := AABB{Min: Vec3{}, Max: Vec3{X: 1, Y: 1, Z: 1}}
	b := AABB{Min: Vec3{X: 3, Y: 0, Z: 0}, Max: Vec3{X: 4, Y: 1, Z: 1}}
	if u := a.Union(b); u.Min != (Vec3{}) || u.Max != (Vec3{X: 4, Y: 1, Z: 1}) {
		t.Fatalf("Union = %v..%v", u.Min, u.Max)
	}
	if f := FatAABB(a, 0.5); f.Min != (Vec3{X: -0.5, Y: -0.5, Z: -0.5}) {
		t.Fatalf("FatAABB.Min = %v", f.Min)
	}
	if sw := SweptAABB(a, Vec3{X: 2, Y: -3}); sw.Min.Y != -3 || sw.Max.X != 3 {
		t.Fatalf("SweptAABB = %v..%v", sw.Min, sw.Max)
	}
	if !a.Contains(AABB{}.Center()) || !a.ContainsBox(AABB{Min: Vec3{X: 0.2}, Max: Vec3{X: 0.8}}) {
		t.Fatal("Contains / ContainsBox 判定错误")
	}
}

func TestVec3Ops(t *testing.T) {
	x, y := Vec3{X: 1}, Vec3{Y: 1}
	if x.Cross(y) != (Vec3{Z: 1}) || x.Dot(y) != 0 {
		t.Fatal("叉积/点积错误")
	}
	if l := (Vec3{X: 3, Y: 4}).Len(); math.Abs(l-5) > 1e-12 {
		t.Fatalf("Len = %v, want 5", l)
	}
	if n := (Vec3{X: 0, Y: 0, Z: 0}).Normalized(); n != (Vec3{}) {
		t.Fatalf("零向量归一化应为零向量，得到 %v", n)
	}
	if m := (Vec3{X: 1, Y: 5, Z: 3}).Min(Vec3{X: 2, Y: 2, Z: 4}); m != (Vec3{X: 1, Y: 2, Z: 3}) {
		t.Fatalf("Min = %v", m)
	}
	if !(Vec3{X: 0.1}).IsZero(0.2) || (Vec3{X: 0.3}).IsZero(0.2) {
		t.Fatal("IsZero 判定错误")
	}
}

func TestOBBLocalRoundTrip(t *testing.T) {
	obb := OBB{
		C: Vec3{X: 1, Y: 2, Z: 3},
		U: [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}},
		E: [3]float64{1, 2, 3},
	}
	p := Vec3{X: 5, Y: -1, Z: 0.5}
	if got := obb.ToWorld(obb.ToLocal(p)); got.Distance(p) > 1e-12 {
		t.Fatalf("局部坐标往返失败: %v != %v", got, p)
	}
}
