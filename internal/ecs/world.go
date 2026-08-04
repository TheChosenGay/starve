package ecs

import (
	"fmt"
	"reflect"
)

// storageLike 是所有组件存储（*sparseSet[T]）实现的统一接口，
// 供 World.DestroyEntity 批量清理与 dirty 标记。
type storageLike interface {
	removeEntity(e Entity)
	hasEntity(e Entity) bool
	componentID() ComponentID
}

// World 是 ECS 模拟内核：实体分配、组件存储（稀疏集）、系统调度、
// 资源与事件/dirty 的持有者。
//
// 确定性纪律（设计文档 §4.4）：
//   - 迭代一律走 Query；内部 map 只做集合/查表，不做遍历
//   - 随机数/时钟由 Resource 提供，禁止全局 rand 与 time.Now()
//   - 实体 ID 密集分配 + 空闲列表复用
type World struct {
	nextID   uint64
	freeIDs  []Entity
	alive    map[Entity]struct{}
	storages map[reflect.Type]any

	resources map[reflect.Type]any
	systems   []systemEntry

	registry *ComponentRegistry

	events []Event
	dirty  []dirtyOp
}

func NewWorld() *World {
	return &World{
		nextID:    uint64(NullEntity), // 0 保留，ID 从 1 开始
		alive:     make(map[Entity]struct{}),
		storages:  make(map[reflect.Type]any),
		resources: make(map[reflect.Type]any),
		registry:  NewComponentRegistry(),
	}
}

// storage 返回组件 T 的稀疏集，首次使用时惰性创建（零注册仪式）。
func storage[T any](w *World) *sparseSet[T] {
	t := reflect.TypeOf((*T)(nil)).Elem()
	st, ok := w.storages[t]
	if !ok {
		w.registry.ensure(t)
		s := newSparseSet[T]()
		s.compID = ComponentID(w.registry.Name(t))
		w.storages[t] = s
		st = s
	}
	return st.(*sparseSet[T])
}

// CreateEntity 分配一个新实体 ID：优先复用空闲列表，否则密集递增。
func (w *World) CreateEntity() Entity {
	var e Entity
	if n := len(w.freeIDs); n > 0 {
		e = w.freeIDs[n-1]
		w.freeIDs = w.freeIDs[:n-1]
	} else {
		w.nextID++
		e = Entity(w.nextID)
	}
	w.alive[e] = struct{}{}
	w.events = append(w.events, Event{Kind: EntityCreated, Entity: e})
	return e
}

// DestroyEntity 销毁实体：清掉其所有组件、回收 ID，并触发事件。
// 重复销毁会 panic（编程错误，尽早暴露）。
func (w *World) DestroyEntity(e Entity) {
	w.requireAlive(e)
	for _, st := range w.storages {
		s := st.(storageLike)
		if s.hasEntity(e) {
			w.markDirty(e, s.componentID())
		}
		s.removeEntity(e)
	}
	delete(w.alive, e)
	w.freeIDs = append(w.freeIDs, e)
	w.events = append(w.events, Event{Kind: EntityDestroyed, Entity: e})
}

// IsAlive 判断实体是否存活。
func (w *World) IsAlive(e Entity) bool {
	_, ok := w.alive[e]
	return ok
}

// EntityCount 返回当前存活实体数。
func (w *World) EntityCount() int { return len(w.alive) }

func (w *World) requireAlive(e Entity) {
	if !w.IsAlive(e) {
		panic(fmt.Sprintf("ecs: entity %d is not alive", e))
	}
}
