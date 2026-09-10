package collide

import "starve/pkg/collide/kind"

// AABB 是轴对齐包围盒，由最小角点和最大角点定义。
type AABB struct {
	Min, Max Vec3
}

// Center 返回盒中心。
func (b AABB) Center() Vec3 { return b.Min.Add(b.Max).Scale(0.5) }

// Extents 返回各轴的半宽。（Max - Min 的一半）
func (b AABB) Extents() Vec3 { return b.Max.Sub(b.Min).Scale(0.5) }

// Contains 判断点是否在盒内（含边界）。
func (b AABB) Contains(p Vec3) bool {
	return p.X >= b.Min.X && p.X <= b.Max.X &&
		p.Y >= b.Min.Y && p.Y <= b.Max.Y &&
		p.Z >= b.Min.Z && p.Z <= b.Max.Z
}

// AABBFromPoints 返回包住所有点的 AABB。
func AABBFromPoints(pts ...Vec3) AABB {
	if len(pts) == 0 {
		return AABB{}
	}
	b := AABB{Min: pts[0], Max: pts[0]}
	for _, p := range pts[1:] {
		b.Min = b.Min.Min(p)
		b.Max = b.Max.Max(p)
	}
	return b
}

// OBB 是有向包围盒：中心 C，三个正交单位轴 U，各轴半宽 E。
type OBB struct {
	C Vec3
	U [3]Vec3
	E [3]float64
}

// AABBToOBB 把 AABB 表示为轴对齐的 OBB。
func AABBToOBB(b AABB) OBB {
	return OBB{
		C: b.Center(),
		U: [3]Vec3{{X: 1}, {Y: 1}, {Z: 1}},
		E: [3]float64{b.Extents().X, b.Extents().Y, b.Extents().Z},
	}
}

// ToLocal 把世界坐标点转换到 OBB 的局部坐标系（原点在盒心，轴为单位轴）。
func (b OBB) ToLocal(p Vec3) Vec3 {
	d := p.Sub(b.C)
	return Vec3{d.Dot(b.U[0]), d.Dot(b.U[1]), d.Dot(b.U[2])}
}

// ToWorld 把 OBB 局部坐标转换回世界坐标。
func (b OBB) ToWorld(l Vec3) Vec3 {
	return b.C.
		Add(b.U[0].Scale(l.X)).
		Add(b.U[1].Scale(l.Y)).
		Add(b.U[2].Scale(l.Z))
}

// Sphere 是球：中心 C，半径 R。
type Sphere struct {
	C Vec3
	R float64
}

// Plane 是平面：满足 N·X = D 的点集。N 不必是单位向量。
type Plane struct {
	N Vec3
	D float64
}

// PlaneFromPoints 用三个不共线点构造平面（逆时针顺序，法向指向观察者）。
func PlaneFromPoints(a, b, c Vec3) Plane {
	n := b.Sub(a).Cross(c.Sub(a))
	return Plane{N: n, D: n.Dot(a)}
}

// Segment 是线段，端点 A、B。
type Segment struct {
	A, B Vec3
}

// Dir 返回 B-A。
func (s Segment) Dir() Vec3 { return s.B.Sub(s.A) }

// Capsule 是胶囊体：中轴线段 A-B 加上半径 R。
// 常用于角色身体/四肢的近似体。
type Capsule struct {
	A, B Vec3
	R    float64
}

// Ray 是射线：原点 + 单位方向 + 最大距离（math.Inf(1) 表示无限）。
type Ray struct {
	Origin Vec3
	Dir    Vec3
	Max    float64
}

// Line 是无限直线：一点 + 方向。
type Line struct {
	Origin Vec3
	Dir    Vec3
}

// Triangle 是三角形。
type Triangle struct {
	V0, V1, V2 Vec3
}

// ---- Kind：编译期常量，零开销 ----

func (Vec3) Kind() kind.Kind     { return kind.Point }
func (Segment) Kind() kind.Kind  { return kind.Segment }
func (Line) Kind() kind.Kind     { return kind.Line }
func (Ray) Kind() kind.Kind      { return kind.Ray }
func (Plane) Kind() kind.Kind    { return kind.Plane }
func (Triangle) Kind() kind.Kind { return kind.Triangle }
func (AABB) Kind() kind.Kind     { return kind.AABB }
func (OBB) Kind() kind.Kind      { return kind.OBB }
func (Sphere) Kind() kind.Kind   { return kind.Sphere }
func (Capsule) Kind() kind.Kind  { return kind.Capsule }

// Shape 供“异类图元混在一个容器里”时使用。
// 同类集合（例如 []AABB）直接用具体类型更好：不用接口、不装箱、顺序访问缓存友好。
type Shape interface {
	Kind() kind.Kind
}
