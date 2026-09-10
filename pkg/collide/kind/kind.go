// Package kind 定义图元类型标识。
//
// 单独成包是为了让常量名不必带 Kind 前缀：kind.AABB 与 collide.AABB（类型）不会冲突。
// 全部是编译期常量，零内存、零运行时开销，也不会出现“AABB 的 Kind 被设成 Sphere”这类状态错误。
package kind

// Kind 是图元类型标识。
type Kind uint8

const (
	Unknown Kind = iota
	Point
	Segment
	Line
	Ray
	Plane
	Triangle
	AABB
	OBB
	Sphere
	Capsule
)

// String 返回可读名字（日志、调试、测试用）。
func (k Kind) String() string {
	switch k {
	case Point:
		return "Point"
	case Segment:
		return "Segment"
	case Line:
		return "Line"
	case Ray:
		return "Ray"
	case Plane:
		return "Plane"
	case Triangle:
		return "Triangle"
	case AABB:
		return "AABB"
	case OBB:
		return "OBB"
	case Sphere:
		return "Sphere"
	case Capsule:
		return "Capsule"
	default:
		return "Unknown"
	}
}
