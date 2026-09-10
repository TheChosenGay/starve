package primitive

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

// ContainsBox 判断 o 是否完全落在 b 内（含边界）。
func (b AABB) ContainsBox(o AABB) bool {
	return b.Min.X <= o.Min.X && b.Min.Y <= o.Min.Y && b.Min.Z <= o.Min.Z &&
		o.Max.X <= b.Max.X && o.Max.Y <= b.Max.Y && o.Max.Z <= b.Max.Z
}

// Union 返回同时包住 b 与 o 的 AABB。
func (b AABB) Union(o AABB) AABB {
	return AABB{Min: b.Min.Min(o.Min), Max: b.Max.Max(o.Max)}
}

// Expand 把盒在各方向外扩 r（r 可为负，表示收缩）。
func (b AABB) Expand(r float64) AABB {
	d := Vec3{X: r, Y: r, Z: r}
	return AABB{Min: b.Min.Sub(d), Max: b.Max.Add(d)}
}

// Translate 返回平移 v 后的盒。
func (b AABB) Translate(v Vec3) AABB { return AABB{Min: b.Min.Add(v), Max: b.Max.Add(v)} }

// Bounds 返回盒自身（AABB 是它自己的紧包围盒）。
func (b AABB) Bounds() AABB { return b }

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

// AABBUnion 是 Union 的函数形式（便于链式表达与 JS 侧对齐）。
func AABBUnion(a, b AABB) AABB { return a.Union(b) }

// FatAABB 返回宽阶段用的外扩盒：索引里存的是它，不是紧盒。
// margin 由调用方决定（静态物可以 0，高速物体要按单帧最大位移外扩）。
func FatAABB(b AABB, margin float64) AABB { return b.Expand(margin) }

// SweptAABB 返回盒 b 沿 motion 平移所扫过体积的包围盒。
// 平移扫掠的包围盒等于两个端点盒的并，精确无采样（旋转扫掠不适用）。
func SweptAABB(b AABB, motion Vec3) AABB { return b.Union(b.Translate(motion)) }

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
	return Vec3{X: d.Dot(b.U[0]), Y: d.Dot(b.U[1]), Z: d.Dot(b.U[2])}
}

// ToWorld 把 OBB 局部坐标转换回世界坐标。
func (b OBB) ToWorld(l Vec3) Vec3 {
	return b.C.
		Add(b.U[0].Scale(l.X)).
		Add(b.U[1].Scale(l.Y)).
		Add(b.U[2].Scale(l.Z))
}

// Bounds 返回 OBB 的紧轴对齐盒：中心 ± 各轴投影半径之和。
func (b OBB) Bounds() AABB {
	r := Vec3{}
	for i := 0; i < 3; i++ {
		r = r.Add(b.U[i].Abs().Scale(b.E[i]))
	}
	return AABB{Min: b.C.Sub(r), Max: b.C.Add(r)}
}

// Sphere 是球：中心 C，半径 R。
type Sphere struct {
	C Vec3
	R float64
}

// Bounds 返回球的盒。
func (s Sphere) Bounds() AABB { return s.C.Bounds().Expand(s.R) }

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

// Bounds 返回线段端点的盒。
func (s Segment) Bounds() AABB { return AABBFromPoints(s.A, s.B) }

// Capsule 是胶囊体：中轴线段 A-B 加上半径 R。
// 常用于角色身体/四肢的近似体。
type Capsule struct {
	A, B Vec3
	R    float64
}

// Bounds 返回胶囊的盒：中轴段包围盒外扩半径。
func (c Capsule) Bounds() AABB { return AABBFromPoints(c.A, c.B).Expand(c.R) }

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

// Bounds 返回三角形顶点的盒。
func (t Triangle) Bounds() AABB { return AABBFromPoints(t.V0, t.V1, t.V2) }

// Bounds 返回点自身的退化盒。
func (v Vec3) Bounds() AABB { return AABB{Min: v, Max: v} }

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

// Shape 是所有图元的公共身份：能报告自己是什么（Kind）。
//
// 同类集合（例如 []AABB）直接用具体类型更好：不用接口、不装箱、顺序访问缓存友好。
// Shape 只在「异类图元混在一个容器里」时才需要。
type Shape interface {
	Kind() kind.Kind
}

// Solid 是有体积、能给出紧包围盒的图元——宽阶段代理的原料。
//
// Ray / Line / Plane 没有有界包围盒，因此不属于 Solid：它们不能作为代理进索引，
// 但可以作为查询体（射线投射）使用。
type Solid interface {
	Shape
	Bounds() AABB
}
