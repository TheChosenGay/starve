package actor

import "time"

// 命名约定：本项目的接口统一以 I 开头（IActor / IActorContext）。

// IActorContext 是业务 actor 与运行时打交道的窗口：
// 当前消息环境（Message / Sender / PID）+ 通信能力
// （Send / Request / Respond / SpawnChild）。
// 具体实现是 *Context，由 process 在每条消息交付前更新并传入 Receive。
type IActorContext interface {
	Message() any
	Sender() *PID
	PID() *PID
	Send(pid *PID, msg any)
	Request(pid *PID, msg any, timeout time.Duration) *Response
	Respond(msg any)
	SpawnChild(producer Producer, name string) *PID
}

// IActor 是引擎持有的业务对象（如 *WorldActor、*AgentActor）。
// 消息按类型分发：Receive 里 switch ctx.Message().(type) 处理，无反射、无注册。
//
//	func (a *worldActor) Receive(ctx actor.IActorContext) {
//	    switch m := ctx.Message().(type) {
//	    case PlayerMove:
//	        a.onMove(m)
//	    }
//	}
type IActor interface {
	Receive(ctx IActorContext)
}

// Producer 是 actor 实例的工厂函数：Spawn 时调用一次创建初始实例，
// 崩溃重启时再次调用以重建（PID 不变、状态重置）。
//
//	engine.Spawn(func() actor.IActor { return &worldActor{} }, "world", "room-1")
type Producer func() IActor
