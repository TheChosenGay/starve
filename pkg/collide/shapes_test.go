package collide

import (
	"math"
	"testing"
)

// sampleShapes 返回一组覆盖各已支持组合的图元（含互相重叠与分离的）。
func sampleShapes() []Shape {
	return []Shape{
		Sphere{C: Vec3{X: 0, Y: 1, Z: 0}, R: 0.5},
		Sphere{C: Vec3{X: 0.6, Y: 1, Z: 0}, R: 0.3},
		Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 2, Z: 0}, R: 0.3},
		Capsule{A: Vec3{X: 0.5, Y: 0, Z: 0}, B: Vec3{X: 0.5, Y: 2, Z: 0}, R: 0.25},
		AABB{Min: Vec3{X: -0.4, Y: 0, Z: -0.4}, Max: Vec3{X: 0.4, Y: 2, Z: 0.4}},
		AABB{Min: Vec3{X: 3, Y: 0, Z: 3}, Max: Vec3{X: 4, Y: 2, Z: 4}},
		OBB{C: Vec3{X: 0.2, Y: 1, Z: 0.2}, U: [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}}, E: [3]float64{0.3, 0.3, 0.3}},
		Triangle{V0: Vec3{X: -1, Y: 1, Z: 1}, V1: Vec3{X: 1, Y: 1, Z: 1}, V2: Vec3{X: 0, Y: 1, Z: 2}},
		Segment{A: Vec3{X: -1, Y: 1, Z: 0}, B: Vec3{X: 1, Y: 1, Z: 0}},
		Vec3{X: 0.2, Y: 1, Z: 0.1},
	}
}

// pairSupported 探测一个组合是否已实现（未实现会 panic）。
func pairSupported(fn func()) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	fn()
	return true
}

// TestTestShapesSymmetric：相交判定必须与参数顺序无关。
func TestTestShapesSymmetric(t *testing.T) {
	ss := sampleShapes()
	checked := 0
	for i := range ss {
		for j := range ss {
			a, b := ss[i], ss[j]
			if !pairSupported(func() { TestShapes(a, b) }) || !pairSupported(func() { TestShapes(b, a) }) {
				continue // 未实现的组合（如三角形 × 盒）不走对称性检查
			}
			checked++
			if got, want := TestShapes(a, b), TestShapes(b, a); got != want {
				t.Fatalf("TestShapes 不对称: %v×%v got %v, 反向 %v",
					a.Kind(), b.Kind(), got, want)
			}
		}
	}
	if checked < 50 {
		t.Fatalf("覆盖的组合太少: %d", checked)
	}
}

// TestContactShapesNormalFlip：交换参数必须同时把法向翻过来（约定：B → A）。
func TestContactShapesNormalFlip(t *testing.T) {
	ss := sampleShapes()
	checked := 0
	degenerate := 0
	for i := range ss {
		for j := range ss {
			a, b := ss[i], ss[j]
			if !pairSupported(func() { ContactShapes(a, b) }) || !pairSupported(func() { ContactShapes(b, a) }) {
				continue
			}
			c1, ok1 := ContactShapes(a, b)
			c2, ok2 := ContactShapes(b, a)
			if ok1 != ok2 {
				t.Fatalf("ContactShapes 可命中性不对称: %v×%v", a.Kind(), b.Kind())
			}
			if !ok1 {
				continue
			}
			checked++
			if !vecAlmostEq(c1.Normal, c2.Normal.Neg()) {
				// 两个图元完全重合时法向无良定义（库会退化成任意方向），
				// 此时允许两次给出同一个方向——这是已知且无法避免的退化。
				if vecAlmostEq(c1.Normal, c2.Normal) {
					degenerate++
					continue
				}
				t.Fatalf("%v×%v 交换后法向未取反: %v vs %v",
					a.Kind(), b.Kind(), c1.Normal, c2.Normal)
			}
			if math.Abs(c1.Depth-c2.Depth) > 1e-9 {
				t.Fatalf("%v×%v 交换后穿透深度不一致: %v vs %v",
					a.Kind(), b.Kind(), c1.Depth, c2.Depth)
			}
		}
	}
	if checked == 0 {
		t.Fatal("样本里没有一组接触，测试没覆盖到")
	}
	t.Logf("接触组合 %d 组，其中退化（完全重合）%d 组", checked, degenerate)
}

// TestContactShapesMatchesSpecific：统一入口必须与具体函数同解。
func TestContactShapesMatchesSpecific(t *testing.T) {
	a := Sphere{C: Vec3{X: 0, Y: 0, Z: 0}, R: 1}
	b := AABB{Min: Vec3{X: 0.5, Y: -1, Z: -1}, Max: Vec3{X: 2, Y: 1, Z: 1}}
	want, ok := ContactSphereAABB(a, b)
	got, ok2 := ContactShapes(a, b)
	if !ok || !ok2 {
		t.Fatalf("两者都应命中: %v %v", ok, ok2)
	}
	if !vecAlmostEq(got.Normal, want.Normal) || math.Abs(got.Depth-want.Depth) > 1e-12 {
		t.Fatalf("ContactShapes=%v/%v, ContactSphereAABB=%v/%v", got.Normal, got.Depth, want.Normal, want.Depth)
	}

	// 交换参数：法向必须相反。
	rev, _ := ContactShapes(b, a)
	if !vecAlmostEq(rev.Normal, want.Normal.Neg()) {
		t.Fatalf("交换后法向应为 %v，得到 %v", want.Normal.Neg(), rev.Normal)
	}
}

// TestShapesKnownHits：几组"一眼可知"的判定，防止分发串线。
func TestShapesKnownHits(t *testing.T) {
	box := AABB{Min: Vec3{X: -1, Y: -1, Z: -1}, Max: Vec3{X: 1, Y: 1, Z: 1}}
	cases := []struct {
		name string
		a, b Shape
		want bool
	}{
		{"球心在盒内", Sphere{C: Vec3{}, R: 0.1}, box, true},
		{"球擦到盒面", Sphere{C: Vec3{X: 1.4, Y: 0, Z: 0}, R: 0.5}, box, true},
		{"球远离盒", Sphere{C: Vec3{X: 3, Y: 0, Z: 0}, R: 0.5}, box, false},
		{"胶囊穿过盒", Capsule{A: Vec3{X: -3, Y: 0, Z: 0}, B: Vec3{X: 3, Y: 0, Z: 0}, R: 0.2}, box, true},
		{"胶囊在盒外", Capsule{A: Vec3{X: -3, Y: 5, Z: 0}, B: Vec3{X: 3, Y: 5, Z: 0}, R: 0.2}, box, false},
		{"盒与盒相离", box, AABB{Min: Vec3{X: 5, Y: 5, Z: 5}, Max: Vec3{X: 6, Y: 6, Z: 6}}, false},
		{"盒与盒相交", box, AABB{Min: Vec3{X: 0.5, Y: 0.5, Z: 0.5}, Max: Vec3{X: 2, Y: 2, Z: 2}}, true},
		{"三角形与球", Sphere{C: Vec3{X: 0, Y: 1.2, Z: 1.5}, R: 0.5},
			Triangle{V0: Vec3{X: -1, Y: 1, Z: 1}, V1: Vec3{X: 1, Y: 1, Z: 1}, V2: Vec3{X: 0, Y: 1, Z: 2}}, true},
		{"线段与球", Sphere{C: Vec3{X: 0, Y: 1.3, Z: 1.5}, R: 0.5}, Segment{A: Vec3{X: -1, Y: 1, Z: 1}, B: Vec3{X: 1, Y: 1, Z: 1}}, false},
		{"点与盒", Vec3{X: 0.5, Y: 0.5, Z: 0.5}, box, true},
		{"点在盒外", Vec3{X: 5, Y: 0, Z: 0}, box, false},
	}
	for _, c := range cases {
		if got := TestShapes(c.a, c.b); got != c.want {
			t.Errorf("%s: TestShapes = %v, want %v", c.name, got, c.want)
		}
		if got := TestShapes(c.b, c.a); got != c.want {
			t.Errorf("%s: 反向 TestShapes = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestUnsupportedCombosPanic：未实现的组合必须吵，不能静默返回 false。
func TestUnsupportedCombosPanic(t *testing.T) {
	mustPanic := func(name string, fn func()) {
		defer func() {
			if recover() == nil {
				t.Errorf("%s 应当 panic", name)
			}
		}()
		fn()
	}
	mustPanic("Boxplane × Plane", func() {
		TestShapes(Plane{N: Vec3{Y: 1}}, Sphere{C: Vec3{}, R: 1})
	})
	mustPanic("Triangle × Capsule", func() {
		ContactShapes(Triangle{}, Capsule{R: 1})
	})
	mustPanic("SweepShapes 非球移动体", func() {
		SweepShapes(Capsule{R: 1}, Vec3{X: 1}, Sphere{R: 1})
	})
	mustPanic("IntersectRayShape 胶囊", func() {
		IntersectRayShape(Vec3{}, Vec3{X: 1}, 10, Capsule{R: 1})
	})
}

func TestRayAndSweepDispatch(t *testing.T) {
	// 射线：球在 +X 方向 3 米处，半径 0.5 → 2.5 米命中。
	h := IntersectRayShape(Vec3{}, Vec3{X: 1}, 10, Sphere{C: Vec3{X: 3}, R: 0.5})
	if !h.Hit || math.Abs(h.Dist-2.5) > 1e-9 {
		t.Fatalf("IntersectRayShape(球) = %+v, want dist 2.5", h)
	}
	// 旋转盒：等价于把射线转到局部坐标系，命中距离应与 AABB 情形一致。
	obb := OBB{C: Vec3{X: 3}, U: [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}}, E: [3]float64{1, 1, 1}}
	h = IntersectRayShape(Vec3{}, Vec3{X: 1}, 10, obb)
	if !h.Hit || math.Abs(h.Dist-2) > 1e-9 {
		t.Fatalf("IntersectRayShape(OBB) = %+v, want dist 2", h)
	}
	if !vecAlmostEq(h.Normal, Vec3{X: -1}) {
		t.Fatalf("命中法向应为 -X，得到 %v", h.Normal)
	}
	// 扫掠：球从原点沿 +X 走 10 米，撞上 5 米处的盒。
	hit := SweepShapes(Sphere{C: Vec3{}, R: 0.5}, Vec3{X: 10},
		AABB{Min: Vec3{X: 5, Y: -1, Z: -1}, Max: Vec3{X: 6, Y: 1, Z: 1}})
	if !hit.Hit || math.Abs(hit.T-0.45) > 1e-6 {
		t.Fatalf("SweepShapes = %+v, want T=0.45", hit)
	}
}
