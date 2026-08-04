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
