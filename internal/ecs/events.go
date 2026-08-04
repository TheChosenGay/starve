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
