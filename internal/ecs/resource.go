package ecs

import (
	"fmt"
	"reflect"
)

// AddResource 注入全局单例（世界时钟、RNG、配置）。
// 约定：传指针（如 AddResource(&rng)），Resource[T]() 返回同一个指针，可直接修改。
func (w *World) AddResource(r any) {
	t := reflect.TypeOf(r)
	if t == nil || t.Kind() != reflect.Pointer {
		panic("ecs: AddResource: 需要传指针（如 AddResource(&clock)），保证 Resource[T] 返回可写的同一实例")
	}
	if _, ok := w.resources[t]; ok {
		panic(fmt.Sprintf("ecs: AddResource: duplicate resource %s", t))
	}
	w.resources[t] = r
}

// Resource 返回注入的资源指针；未注入时 panic。
// 约定：AddResource(&value)，Resource[T](w) 按值类型 T 取。
func Resource[T any](w *World) *T {
	t := reflect.TypeOf((*T)(nil))
	r, ok := w.resources[t]
	if !ok {
		panic(fmt.Sprintf("ecs: resource %T 未注入（AddResource(&value)）", (*T)(nil)))
	}
	return r.(*T)
}

// TryResource 安全读取资源：未注入时返回 false（不 panic）。
func TryResource[T any](w *World) (*T, bool) {
	t := reflect.TypeOf((*T)(nil))
	r, ok := w.resources[t]
	if !ok {
		return nil, false
	}
	return r.(*T), true
}

// TryResourceOf 按接口查找已注入资源：返回第一个实现了 T 的资源。
// 生命周期钩子等低频路径用（组件包不能引用资源的具体类型时，靠抽象接口解耦）；
// 热路径不要用（遍历资源表 + reflect）。
func TryResourceOf[T any](w *World) (T, bool) {
	var zero T
	iface := reflect.TypeOf(&zero).Elem()
	for _, r := range w.resources {
		if reflect.TypeOf(r).Implements(iface) {
			return r.(T), true
		}
	}
	return zero, false
}
