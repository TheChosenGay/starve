package components

import "starve/internal/ecs"

import game "starve/pkg/proto/game"

// InterruptReason 是动作取消的有界语义原因。
type InterruptReason = game.ActionOutcomeReason

// Interruptable 可打断行为组件接口：被打断时组件自己恢复世界状态。
// 新可打断行为（蓄力、吟唱等）：组件实现 Resume + init 里 RegisterInterruptable[T]()。
type Interruptable interface {
	// Resume 恢复被打断前的状态（如退回制作材料）。
	Resume(w *ecs.World, e ecs.Entity, reason InterruptReason)
}

type interruptibleProbe func(w *ecs.World, e ecs.Entity) (Interruptable, func(), bool)

// interruptibleProbes 类型擦除的可打断组件探针（init 注册，world 层遍历检查）。
var interruptibleProbes []interruptibleProbe

// RegisterInterruptable 登记一个可打断组件类型（组件包内 init 调用）。
func RegisterInterruptable[T any]() {
	interruptibleProbes = append(interruptibleProbes, func(w *ecs.World, e ecs.Entity) (Interruptable, func(), bool) {
		if !ecs.Has[T](w, e) {
			return nil, nil, false
		}
		c := *ecs.Get[T](w, e) // 值拷贝：先 Resume 发语义/退款，再统一 Remove
		it, ok := any(&c).(Interruptable)
		if !ok {
			return nil, nil, false
		}
		if gate, ok := any(&c).(interface{ CanInterrupt() bool }); ok && !gate.CanInterrupt() {
			return nil, nil, false
		}
		return it, func() { ecs.Remove[T](w, e) }, true
	})
}

// Interruptibles 返回全部可打断组件探针（world 层检查用）。
func Interruptibles() []interruptibleProbe {
	return interruptibleProbes
}

// TryInterrupt 打断实体当前的全部可打断组件。
// 同一实体可同时拥有 ActionState 与 Crafting：两者都必须移除，而退款只由 Crafting.Resume 执行一次。
func TryInterrupt(w *ecs.World, e ecs.Entity, reason InterruptReason) bool {
	interrupted := false
	for _, probe := range Interruptibles() {
		if it, remove, ok := probe(w, e); ok {
			it.Resume(w, e, reason)
			remove()
			interrupted = true
		}
	}
	return interrupted
}
