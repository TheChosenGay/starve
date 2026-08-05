package actor

// Actor 是引擎持有的业务对象（如 *WorldActor、*AgentActor）。
//
// 行为契约尚未定稿（讨论中）：可能包含 Receive(ctx *Context) 之类的消息
// 处理方法。先用空接口占位，Producer.Produce() 返回它，引擎只负责持有
// 实例与生命周期，不依赖其具体形态。
type Actor interface{}

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
