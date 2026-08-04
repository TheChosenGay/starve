package ecs

import "fmt"

// 说明：Go 不支持泛型方法，设计草案中的 w.Add[T](...) 以包级泛型函数
// 实现：Add[T](w, e, c)。类型安全不变，只是调用形态从方法变成函数。

// Add 为实体挂上组件。组件类型 T 首次使用时惰性创建存储。
// 实体不存在或组件已存在时 panic（编程错误，尽早暴露）。
func Add[T any](w *World, e Entity, c T) {
	w.requireAlive(e)
	s := storage[T](w)
	if s.Has(e) {
		panic(fmt.Sprintf("ecs: Add[%s]: entity %d already has component", s.typeName, e))
	}
	s.Add(e, c)
	w.markDirty(e, s.compID)
	w.events = append(w.events, Event{Kind: ComponentAdded, Entity: e, Component: s.compID})
}

// Set 覆盖实体组件值并自动标记 dirty（推荐的可写入口）。
// 实体不存在或组件缺失时 panic；创建组件用 Add。
func Set[T any](w *World, e Entity, c T) {
	w.requireAlive(e)
	s := storage[T](w)
	if !s.Has(e) {
		panic(fmt.Sprintf("ecs: Set[%s]: entity %d has no component", s.typeName, e))
	}
	s.dense[s.index(e)] = c
	w.markDirty(e, s.compID)
}

// Get 返回组件指针（指向稀疏集内部存储，直接修改即生效）。
// 实体不存在或组件缺失时 panic。
func Get[T any](w *World, e Entity) *T {
	w.requireAlive(e)
	return storage[T](w).Get(e)
}

// Has 判断实体是否拥有组件。实体不存在时返回 false。
func Has[T any](w *World, e Entity) bool {
	if !w.IsAlive(e) {
		return false
	}
	return storage[T](w).Has(e)
}

// Remove 移除实体组件并自动标记 dirty。组件不存在时幂等。
func Remove[T any](w *World, e Entity) {
	w.requireAlive(e)
	s := storage[T](w)
	if !s.Has(e) {
		return
	}
	s.Remove(e)
	w.markDirty(e, s.compID)
	w.events = append(w.events, Event{Kind: ComponentRemoved, Entity: e, Component: s.compID})
}
