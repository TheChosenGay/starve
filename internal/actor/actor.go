package actor

// Actor 是引擎持有的业务对象（如 *WorldActor、*AgentActor）。
// 消息按类型分发：Receive 里 switch msg.(type) 处理，无反射、无注册。
//
//	func (a *worldActor) Receive(msg any) {
//	    switch m := msg.(type) {
//	    case PlayerMove:
//	        a.onMove(m)
//	    }
//	}
type Actor interface {
	Receive(msg any)
}

// ContextSetter 是可选接口：实现了它的 actor 在实例创建后、每条消息交付前
// 会拿到当前 Context（用于 Sender / Respond / Request / SpawnChild 等）。
// 不实现的 actor 就是纯消息处理器，不依赖任何上下文。
type ContextSetter interface {
	SetContext(ctx *Context)
}

// Producer 是 actor 实例的工厂：Spawn 时调用一次创建初始实例，
// 崩溃重启时再次调用以重建（PID 不变、状态重置）。
type Producer interface {
	Produce() Actor
}

// ProducerFunc 是 Producer 的函数适配器，方便直接传闭包：
//
//	engine.Spawn(actor.ProducerFunc(func() actor.Actor { return &worldActor{} }), "world", "room-1")
type ProducerFunc func() Actor

// Produce 实现 Producer 接口。
func (f ProducerFunc) Produce() Actor { return f() }
