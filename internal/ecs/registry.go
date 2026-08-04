package ecs

import (
	"reflect"
	"sort"
)

// ComponentID 是组件的稳定标识，用于 dirty 集合、快照与存档。
// 取组件名（如 "Health"），跨世界/跨版本稳定、自描述。
type ComponentID string

// Codec 是组件的编解码器契约（存档/快照/配表共用，M5 起用）。
type Codec[T any] interface {
	Encode(v T) ([]byte, error)
	Decode(b []byte) (T, error)
}

// ComponentMeta 是组件的元数据：名称 + 可选编解码器。
type ComponentMeta struct {
	Name  ComponentID // 稳定标识（组件名），如 "Health"
	Codec any         // Codec[T]
}

// ComponentRegistry 惰性登记组件的名称与编解码器。
// 未显式注册时，名称取 Go 类型名（reflect.Type.Name()）。
type ComponentRegistry struct {
	metas map[reflect.Type]ComponentMeta
}

func NewComponentRegistry() *ComponentRegistry {
	return &ComponentRegistry{metas: make(map[reflect.Type]ComponentMeta)}
}

// ensure 登记一个类型（保留已显式注册的元数据）。
func (r *ComponentRegistry) ensure(t reflect.Type) {
	if _, ok := r.metas[t]; !ok {
		r.metas[t] = ComponentMeta{Name: ComponentID(t.Name())}
	}
}

// Register 显式注册组件元数据（可覆盖默认名称、挂编解码器）。
// 注意：要在组件类型第一次被 Add/Query 使用之前调用。
func Register[T any](r *ComponentRegistry, name string, codec ...Codec[T]) {
	t := reflect.TypeOf((*T)(nil)).Elem()
	m := ComponentMeta{Name: ComponentID(name)}
	if len(codec) > 0 {
		m.Codec = codec[0]
	}
	r.metas[t] = m
}

// Name 返回组件类型的名称：显式注册优先，否则取类型名。
func (r *ComponentRegistry) Name(t reflect.Type) ComponentID {
	if m, ok := r.metas[t]; ok {
		return m.Name
	}
	return ComponentID(t.Name())
}

// Meta 返回组件元数据。
func (r *ComponentRegistry) Meta(t reflect.Type) (ComponentMeta, bool) {
	m, ok := r.metas[t]
	return m, ok
}

// Types 返回已登记（被使用过或显式注册）的组件类型，按名称排序。
// 供存档/快照遍历组件表使用；排序保证遍历顺序确定。
func (r *ComponentRegistry) Types() []reflect.Type {
	ts := make([]reflect.Type, 0, len(r.metas))
	for t := range r.metas {
		ts = append(ts, t)
	}
	sort.Slice(ts, func(i, j int) bool { return r.Name(ts[i]) < r.Name(ts[j]) })
	return ts
}

// RegisterComponent 是 World 上的注册入口：组件类型已被使用时再注册会 panic，
// 防止名称不一致（已有存储快照了旧名称）。
func RegisterComponent[T any](w *World, name string, codec ...Codec[T]) {
	t := reflect.TypeOf((*T)(nil)).Elem()
	if _, ok := w.storages[t]; ok {
		panic("ecs: RegisterComponent: 组件类型已被使用，注册需在第一次 Add/Query 之前")
	}
	Register[T](w.registry, name, codec...)
}

// ComponentIDOf 返回组件 T 的稳定标识（组件名），如 "Health"。
func ComponentIDOf[T any](w *World) ComponentID {
	t := reflect.TypeOf((*T)(nil)).Elem()
	w.registry.ensure(t)
	return w.registry.Name(t)
}

// Registry 返回组件元数据注册表（存档/快照遍历用）。
func (w *World) Registry() *ComponentRegistry { return w.registry }
