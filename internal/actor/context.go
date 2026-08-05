package actor

import "time"

// Context 是 IActorContext 的具体实现：当前消息的上下文。
// 引擎在处理每条消息前更新内部状态，并把同一个 Context 传给 Receive，
// actor 在 Receive 内通过它访问发送者、回复请求、发新消息等。
// 每个 process 一个 Context，跨消息复用（只更新当前消息）。
type Context struct {
	engine *Engine
	proc   *process
	env    envelope
}

// Message 返回当前正在处理的消息。
func (c *Context) Message() any { return c.env.msg }

// Sender 返回消息发送者的 PID；引擎外部 Send 的消息为 nil。
func (c *Context) Sender() *PID { return c.env.sender }

// PID 返回本 actor 的 PID。
func (c *Context) PID() *PID {
	pid := c.proc.pid
	return &pid
}

// Send 异步投递消息（fire-and-forget）。
func (c *Context) Send(pid *PID, msg any) { c.engine.Send(pid, msg) }

// Request 请求-应答：带超时投递，返回 *Response（调 Wait 才阻塞）。
// 纪律：世界 actor 的 tick 内不要 Wait。
func (c *Context) Request(pid *PID, msg any, timeout time.Duration) *Response {
	return c.engine.requestFrom(pid, msg, timeout, &c.proc.pid)
}

// Respond 回复当前消息的请求方（仅当当前消息是请求时有效）。
// 非请求消息上调用会记日志忽略；超时后的迟到回复会被引擎丢弃。
func (c *Context) Respond(msg any) {
	if c.env.requestID == 0 {
		c.engine.logger.Warn("actor: Respond without request", "pid", c.proc.pid)
		return
	}
	c.engine.completeRequest(c.env.requestID, msg)
}

// SpawnChild 在当前 actor 下派生子 actor，子 actor 受父 actor 监督
// （父永久停止时递归停止）。子 PID 形如 "parent.ID.childName"。
func (c *Context) SpawnChild(producer Producer, name string) *PID {
	return c.engine.spawnChild(c.proc, producer, name)
}
