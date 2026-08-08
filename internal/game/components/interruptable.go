package components

// Interruptable 可打断行为组件接口：实体被打断（受击/行动）时，
// 组件返回恢复所需的关键信息（如制作的配方 id）。
// 新可打断行为（蓄力、吟唱等）：组件实现本接口 + 在 world 的可打断清单里加一项。
type Interruptable interface {
	InterruptKey() string
}
