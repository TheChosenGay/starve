package ecs

import (
	"fmt"
	"reflect"
)

// sparseSet 是组件 T 的稀疏集存储：
//
//	sparse:   下标 = 实体ID，值 = dense 下标（-1 表示不存在）
//	dense:    紧凑连续的数据数组（真正的组件数据）
//	entities: dense[i] 对应的实体 ID
//
// 特性：
//   - Add / Get / Has / Remove 均为 O(1)
//   - 遍历顺序 = dense 顺序，确定性
//   - Remove 用交换删除（最后一个元素挪到被删位置），删除后遍历不再是严格
//     实体 ID 升序，但"同样的操作序列产生同样的结果"仍然成立
type sparseSet[T any] struct {
	sparse   []int
	dense    []T
	entities []Entity

	compID   ComponentID
	typeName string
}

func newSparseSet[T any]() *sparseSet[T] {
	return &sparseSet[T]{
		typeName: reflect.TypeOf((*T)(nil)).Elem().String(),
	}
}

// grow 确保 sparse 覆盖实体 ID e。
func (s *sparseSet[T]) grow(e Entity) {
	need := int(e) + 1
	if need <= len(s.sparse) {
		return
	}
	size := need
	if size < len(s.sparse)*2 {
		size = len(s.sparse) * 2
	}
	grown := make([]int, size)
	copy(grown, s.sparse)
	for i := len(s.sparse); i < size; i++ {
		grown[i] = -1
	}
	s.sparse = grown
}

func (s *sparseSet[T]) index(e Entity) int {
	if int(e) >= len(s.sparse) {
		return -1
	}
	return s.sparse[e]
}

// Add 添加组件，实体已有该组件时 panic。
func (s *sparseSet[T]) Add(e Entity, v T) {
	s.grow(e)
	if s.sparse[e] >= 0 {
		panic(fmt.Sprintf("ecs: %s already exists on entity %d", s.typeName, e))
	}
	s.sparse[e] = len(s.dense)
	s.dense = append(s.dense, v)
	s.entities = append(s.entities, e)
}

// Get 返回组件指针（指向 dense 内部存储，直接修改即生效）。
// 组件缺失时 panic；不确定时先用 Has 判断。
func (s *sparseSet[T]) Get(e Entity) *T {
	idx := s.index(e)
	if idx < 0 {
		panic(fmt.Sprintf("ecs: entity %d has no %s component", e, s.typeName))
	}
	return &s.dense[idx]
}

// tryGet 返回组件指针，缺失时返回 nil（Query2 反查用，不 panic）。
func (s *sparseSet[T]) tryGet(e Entity) *T {
	idx := s.index(e)
	if idx < 0 {
		return nil
	}
	return &s.dense[idx]
}

// Has 判断实体是否拥有该组件。
func (s *sparseSet[T]) Has(e Entity) bool {
	return s.index(e) >= 0
}

// Remove 移除组件（交换删除）。组件不存在时幂等。
func (s *sparseSet[T]) Remove(e Entity) {
	idx := s.index(e)
	if idx < 0 {
		return
	}
	last := len(s.dense) - 1
	if idx != last {
		lastEnt := s.entities[last]
		s.dense[idx] = s.dense[last]
		s.entities[idx] = lastEnt
		s.sparse[lastEnt] = idx
	}
	s.sparse[e] = -1
	s.dense = s.dense[:last]
	s.entities = s.entities[:last]
}

// Len 返回当前实例数。
func (s *sparseSet[T]) Len() int { return len(s.dense) }

// Dense 返回数据数组（只读遍历，不要修改）。
func (s *sparseSet[T]) Dense() []T { return s.dense }

// Entities 返回 dense[i] 对应的实体 ID（只读遍历，不要修改）。
func (s *sparseSet[T]) Entities() []Entity { return s.entities }

// 以下三个方法供 World 批量清理与 dirty 标记（storageLike 接口）。
func (s *sparseSet[T]) removeEntity(e Entity)    { s.Remove(e) }
func (s *sparseSet[T]) hasEntity(e Entity) bool  { return s.Has(e) }
func (s *sparseSet[T]) componentID() ComponentID { return s.compID }
