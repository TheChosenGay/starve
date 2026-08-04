package ecs

// Query 遍历所有拥有组件 T 的实体，顺序 = 稀疏集 dense 顺序（确定性）。
// 回调内不要增删组件 T 本身（扩容会让遍历语义不确定）。
func Query[T any](w *World, fn func(e Entity, c *T)) {
	s := storage[T](w)
	for i := range s.dense {
		fn(s.entities[i], &s.dense[i])
	}
}

// Query2 遍历同时拥有 A、B 两种组件的实体。
// 遍历两个稀疏集中较小的那个，另一个用 sparse 表 O(1) 反查。
func Query2[A, B any](w *World, fn func(e Entity, a *A, b *B)) {
	sa, sb := storage[A](w), storage[B](w)
	if sa.Len() <= sb.Len() {
		for i := range sa.dense {
			e := sa.entities[i]
			if b := sb.tryGet(e); b != nil {
				fn(e, &sa.dense[i], b)
			}
		}
		return
	}
	for i := range sb.dense {
		e := sb.entities[i]
		if a := sa.tryGet(e); a != nil {
			fn(e, a, &sb.dense[i])
		}
	}
}
