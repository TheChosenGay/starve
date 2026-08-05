package actor

// 命名约定：本项目的接口统一以 I 开头（IActor / IContextSetter），
// 与既有代码风格区分（用户约定）。

// IActor 是引擎持有的业务对象（如 *WorldActor、*AgentActor）。
// 消息按类型分发：Receive 里 switch msg.(type) 处理，无反射、无注册。
//
//	func (a *worldActor) Receive(msg any) {
//	    switch m := msg.(type) {
//	    case PlayerMove:
//	        a.onMove(m)
//	    }
//	}
type IActor interface {
	Receive(msg any)
}

// IContextSetter 是可选接口：实现了它的 actor 在实例创建后、每条消息交付前
// 会拿到当前 Context（用于 Sender / Respond / Request / SpawnChild 等）。
// 不实现的 actor 就是纯消息处理器，不依赖任何上下文。
type IContextSetter interface {
	SetContext(ctx *Context)
}

// Producer 是 actor 实例的工厂函数：Spawn 时调用一次创建初始实例，
// 崩溃重启时再次调用以重建（PID 不变、状态重置）。
//
//	engine.Spawn(func() actor.IActor { return &worldActor{} }, "world", "room-1")
type Producer func() IActor
