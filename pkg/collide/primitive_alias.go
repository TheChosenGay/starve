package collide

import "starve/pkg/collide/primitive"

// 图元的定义在 primitive 子包，这里用**类型别名**重导出。
//
// 为什么要别名而不是复制定义：别名是同一个类型，方法集、可比较性、值语义完全一致，
// 但只存在一份定义（和标准库 os.FileMode = fs.FileMode 是同一个手法）。
// 好处是调用方两个名字都能用——只做几何计算时写 primitive.Sphere，
// 走碰撞库时写 collide.Sphere——而不会产生"两套 Sphere"的转换地狱。
type (
	Vec3     = primitive.Vec3
	AABB     = primitive.AABB
	OBB      = primitive.OBB
	Sphere   = primitive.Sphere
	Capsule  = primitive.Capsule
	Segment  = primitive.Segment
	Triangle = primitive.Triangle
	Ray      = primitive.Ray
	Line     = primitive.Line
	Plane    = primitive.Plane

	// Shape 是所有图元的公共身份；Solid 是其中有体积、能给出包围盒的那些。
	Shape = primitive.Shape
	Solid = primitive.Solid
)

// Zero 是零向量。
var Zero = primitive.Zero

// 下面这些是图元构造/盒运算的转发。规范定义在 primitive 包，
// 这里保留同名入口，让「只用 collide 一个包」的调用方不必来回切换 import。

// AABBFromPoints 返回包住所有点的 AABB。
func AABBFromPoints(pts ...Vec3) AABB { return primitive.AABBFromPoints(pts...) }

// AABBToOBB 把 AABB 表示为轴对齐的 OBB。
func AABBToOBB(b AABB) OBB { return primitive.AABBToOBB(b) }

// PlaneFromPoints 用三个不共线点构造平面。
func PlaneFromPoints(a, b, c Vec3) Plane { return primitive.PlaneFromPoints(a, b, c) }

// AABBUnion 返回同时包住 a 与 b 的盒。
func AABBUnion(a, b AABB) AABB { return primitive.AABBUnion(a, b) }

// FatAABB 返回宽阶段用的外扩盒（索引里存的是它，不是紧盒）。
func FatAABB(b AABB, margin float64) AABB { return primitive.FatAABB(b, margin) }

// SweptAABB 返回盒沿 motion 平移所扫过体积的包围盒。
func SweptAABB(b AABB, motion Vec3) AABB { return primitive.SweptAABB(b, motion) }

// clamp 把 x 限制到 [lo, hi]。
func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
