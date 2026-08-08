package ecs

// EventKind 标识结构变更事件的类型。
type EventKind uint8

const (
	EntityCreated EventKind = iota
	EntityDestroyed
	ComponentAdded
	ComponentRemoved
)

func (k EventKind) String() string {
	switch k {
	case EntityCreated:
		return "EntityCreated"
	case EntityDestroyed:
		return "EntityDestroyed"
	case ComponentAdded:
		return "ComponentAdded"
	case ComponentRemoved:
		return "ComponentRemoved"
	}
	return "Unknown"
}

// Event 是结构变更事件（组件增删/实体创建销毁），按产生顺序入队，tick 内消费。
type Event struct {
	Kind      EventKind
	Entity    Entity
	Component ComponentID // 仅 ComponentAdded / ComponentRemoved 有效
}

// DrainEvents 取走并清空事件队列（保持产生顺序，确定性）。
func (w *World) DrainEvents() []Event {
	evs := w.events
	w.events = nil
	return evs
}

// Emit 发射一个副作用（如打断/完成通知）。组件等纯数据层用此把“要通知外部”的
// 意图交给世界 actor 在 tick 边界统一翻译成推送——组件不直接依赖 actor/outbox。
func (w *World) Emit(effect any) {
	w.effects = append(w.effects, effect)
}

// DrainEffects 取走并清空副作用队列（保持产生顺序，确定性）。
func (w *World) DrainEffects() []any {
	efs := w.effects
	w.effects = nil
	return efs
}
