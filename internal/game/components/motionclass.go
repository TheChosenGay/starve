package components

import (
	"starve/internal/ecs"
)

// Static 静态实体标记：位置永不变化（树/岩/建筑/工作站）。
//
// 用途：碰撞求解时区分"能不能被推动"。静态实体的形状注册进碰撞索引后不再更新，
// 扫掠解算把它当**硬约束**（绝不穿模），也不会因为碰撞而移动。
//
// 空组件（tag）：只表达存在性，不带数据。
type Static struct{}

// Dynamic 动态实体标记：位置每 tick 可能变化（玩家/动物）。
//
// 用途：这些实体每 tick 把形状刷新到最新位置；它们之间用 ORCA 互相避让
// （软约束，会为对方让路），而不是像静态障碍那样硬挡。
//
// 与 Static 互斥：一个实体只能属于其一（见 ValidateMotionClass）。
type Dynamic struct{}

// MotionClass 返回实体的运动类别（供碰撞系统快速分侧）。
type MotionClass uint8

const (
	// MotionClassNone 既不是 Static 也不是 Dynamic（不参与碰撞求解）。
	MotionClassNone MotionClass = iota
	// MotionClassStatic 静态。
	MotionClassStatic
	// MotionClassDynamic 动态。
	MotionClassDynamic
	// MotionClassBoth 同时挂了 Static 与 Dynamic（非法状态）。
	MotionClassBoth
)

// MotionClassOf 读出一个实体的运动类别。
func MotionClassOf(w *ecs.World, e ecs.Entity) MotionClass {
	isStatic := ecs.Has[Static](w, e)
	isDynamic := ecs.Has[Dynamic](w, e)
	switch {
	case isStatic && isDynamic:
		return MotionClassBoth
	case isStatic:
		return MotionClassStatic
	case isDynamic:
		return MotionClassDynamic
	default:
		return MotionClassNone
	}
}

// IsStatic 便利判断。
func IsStatic(w *ecs.World, e ecs.Entity) bool { return ecs.Has[Static](w, e) }

// IsDynamic 便利判断。
func IsDynamic(w *ecs.World, e ecs.Entity) bool { return ecs.Has[Dynamic](w, e) }

func RegisterStatic(w *ecs.World)  { ecs.RegisterComponent(w, "Static", staticCodec{}) }
func RegisterDynamic(w *ecs.World) { ecs.RegisterComponent(w, "Dynamic", dynamicCodec{}) }

// 空组件也需要 codec（进存档/快照），编解码都是空字节。
type staticCodec struct{}

func (staticCodec) Encode(Static) ([]byte, error) { return nil, nil }
func (staticCodec) Decode([]byte) (Static, error) { return Static{}, nil }

type dynamicCodec struct{}

func (dynamicCodec) Encode(Dynamic) ([]byte, error) { return nil, nil }
func (dynamicCodec) Decode([]byte) (Dynamic, error) { return Dynamic{}, nil }
