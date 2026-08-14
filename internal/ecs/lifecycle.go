package ecs

// ILifecycleAdd 组件生命周期：挂载后回调。
// 实现方可在这里做"组件就位后"的联动（如 Block 挂载后写地图阻挡层）。
// 注意：依赖组件（如 Position）必须先于本组件 Add，回调里才能读到。
type ILifecycleAdd interface {
	OnAdd(w *World, e Entity)
}

// ILifecycleRemove 组件生命周期：移除前回调（实体销毁时同样触发）。
// 回调执行时组件自身与其他组件仍完整可读，适合做"移除前清理"（如 Block 清除占格）。
type ILifecycleRemove interface {
	OnRemove(w *World, e Entity)
}
