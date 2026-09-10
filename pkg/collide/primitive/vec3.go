package primitive

import "math"

// Vec3 是三维向量/点。约定右手系，单位由调用方决定。
type Vec3 struct {
	X, Y, Z float64
}

// Zero 是零向量。
var Zero = Vec3{}

func (v Vec3) Add(o Vec3) Vec3      { return Vec3{X: v.X + o.X, Y: v.Y + o.Y, Z: v.Z + o.Z} }
func (v Vec3) Sub(o Vec3) Vec3      { return Vec3{X: v.X - o.X, Y: v.Y - o.Y, Z: v.Z - o.Z} }
func (v Vec3) Neg() Vec3            { return Vec3{X: -v.X, Y: -v.Y, Z: -v.Z} }
func (v Vec3) Scale(s float64) Vec3 { return Vec3{X: v.X * s, Y: v.Y * s, Z: v.Z * s} }

// Dot 是点积。
func (v Vec3) Dot(o Vec3) float64 { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }

// Cross 是叉积。
func (v Vec3) Cross(o Vec3) Vec3 {
	return Vec3{X: v.Y*o.Z - v.Z*o.Y, Y: v.Z*o.X - v.X*o.Z, Z: v.X*o.Y - v.Y*o.X}
}

// LenSq 是长度平方。
func (v Vec3) LenSq() float64 { return v.Dot(v) }

// Len 是长度。
func (v Vec3) Len() float64 { return math.Sqrt(v.LenSq()) }

// DistanceSq 是与另一点的平方距离。
func (v Vec3) DistanceSq(o Vec3) float64 { return v.Sub(o).LenSq() }

// Distance 是与另一点的距离。
func (v Vec3) Distance(o Vec3) float64 { return math.Sqrt(v.DistanceSq(o)) }

// Normalized 返回单位向量；零向量返回零向量。
func (v Vec3) Normalized() Vec3 {
	l := v.Len()
	if l == 0 {
		return Vec3{}
	}
	return v.Scale(1 / l)
}

// Abs 返回逐分量绝对值的向量。
func (v Vec3) Abs() Vec3 {
	return Vec3{X: math.Abs(v.X), Y: math.Abs(v.Y), Z: math.Abs(v.Z)}
}

// Min 返回逐分量最小值。
func (v Vec3) Min(o Vec3) Vec3 {
	return Vec3{X: math.Min(v.X, o.X), Y: math.Min(v.Y, o.Y), Z: math.Min(v.Z, o.Z)}
}

// Max 返回逐分量最大值。
func (v Vec3) Max(o Vec3) Vec3 {
	return Vec3{X: math.Max(v.X, o.X), Y: math.Max(v.Y, o.Y), Z: math.Max(v.Z, o.Z)}
}

// IsZero 判断向量各分量绝对值是否都在 eps 内。
func (v Vec3) IsZero(eps float64) bool {
	return math.Abs(v.X) <= eps && math.Abs(v.Y) <= eps && math.Abs(v.Z) <= eps
}

// Lerp 是线性插值：t=0 返回 v，t=1 返回 o。
func (v Vec3) Lerp(o Vec3, t float64) Vec3 { return v.Add(o.Sub(v).Scale(t)) }

// At 返回第 i 个分量（0=X, 1=Y, 2=Z）。
func (v Vec3) At(i int) float64 {
	switch i {
	case 0:
		return v.X
	case 1:
		return v.Y
	default:
		return v.Z
	}
}

// WithAt 返回把第 i 个分量替换成 x 的副本。
func (v Vec3) WithAt(i int, x float64) Vec3 {
	switch i {
	case 0:
		return Vec3{X: x, Y: v.Y, Z: v.Z}
	case 1:
		return Vec3{X: v.X, Y: x, Z: v.Z}
	default:
		return Vec3{X: v.X, Y: v.Y, Z: x}
	}
}
