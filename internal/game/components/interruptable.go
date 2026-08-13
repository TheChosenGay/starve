package components

import "starve/internal/ecs"

// Interruptable 可打断行为组件接口：被打断时组件自己恢复世界状态。
// 新可打断行为（蓄力、吟唱等）：组件实现 Resume + init 里 RegisterInterruptable[T]()。
type Interruptable interface {
	// Resume 恢复被打断前的状态（如退回制作材料）。
	Resume(w *ecs.World, e ecs.Entity)
}

// interruptibleProbes 类型擦除的可打断组件探针（init 注册，world 层遍历检查）。
var interruptibleProbes []func(w *ecs.World, e ecs.Entity) (Interruptable, bool)

// RegisterInterruptable 登记一个可打断组件类型（组件包内 init 调用）。
func RegisterInterruptable[T any]() {
	interruptibleProbes = append(interruptibleProbes, func(w *ecs.World, e ecs.Entity) (Interruptable, bool) {
		if !ecs.Has[T](w, e) {
			return nil, false
		}
		c := *ecs.Get[T](w, e) // 值拷贝：Remove 前取走，之后实例仍可用
		it, ok := any(&c).(Interruptable)
		if !ok {
			return nil, false
		}
		ecs.Remove[T](w, e)
		return it, true
	})
}

// Interruptibles 返回全部可打断组件探针（world 层检查用）。
func Interruptibles() []func(w *ecs.World, e ecs.Entity) (Interruptable, bool) {
	return interruptibleProbes
}

// TryInterrupt 尝试打断实体当前的可打断行为（如制作）：命中则移除组件并执行 Resume。
// 供统一攻击结算（systems.ApplyAttack）与命令层共用。
func TryInterrupt(w *ecs.World, e ecs.Entity) bool {
	for _, probe := range Interruptibles() {
		if it, ok := probe(w, e); ok {
			it.Resume(w, e)
			return true
		}
	}
	return false
}
