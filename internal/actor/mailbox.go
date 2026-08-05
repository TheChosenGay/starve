package actor

import (
	"errors"
	"sync"
	"time"
)

// 邮箱相关错误。
var (
	ErrMailboxClosed  = errors.New("actor: mailbox closed")
	ErrMailboxTimeout = errors.New("actor: mailbox push timeout")
)

// envelope 是邮箱里的一条消息：业务消息 + 发送者 + 可选的请求上下文。
// requestID 为 0 表示普通消息，非 0 表示这是一次请求（Request），目标 actor
// 处理完用 ctx.Respond 回复时靠它路由。
type envelope struct {
	msg       any
	sender    *PID
	requestID uint64
}

// mailbox 是有界 FIFO 邮箱：actor 的收件箱（inbox）。
//
// 实现说明：设计文档写的是手写 ringbuffer；这里改用有界 channel——
// 外部语义一致（有界、FIFO、批量取出、满时背压），但能原生支持
// pushTimeout 的限时入队（ringbuffer + 条件变量做不了可中断的定时等待）。
// 并发模型：N 个发送者 push、1 个处理 goroutine popBatch；close 唤醒
// 所有等待者，并丢弃剩余消息。
type mailbox struct {
	ch        chan envelope // 有界 FIFO 队列
	closedCh  chan struct{} // close 时关闭，唤醒所有阻塞的 push/pop
	closeOnce sync.Once
}

func newMailbox(capacity int) *mailbox {
	if capacity <= 0 {
		capacity = 1
	}
	return &mailbox{
		ch:       make(chan envelope, capacity),
		closedCh: make(chan struct{}),
	}
}

// push 投递一条消息：满时阻塞（背压），邮箱关闭后返回 ErrMailboxClosed。
func (m *mailbox) push(env envelope) error {
	select {
	case m.ch <- env:
		return nil
	case <-m.closedCh:
		return ErrMailboxClosed
	}
}

// pushTimeout 投递一条消息，满时最多等 timeout；超时返回 ErrMailboxTimeout。
func (m *mailbox) pushTimeout(env envelope, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case m.ch <- env:
		return nil
	case <-m.closedCh:
		return ErrMailboxClosed
	case <-timer.C:
		return ErrMailboxTimeout
	}
}

// popBatch 一次取出最多 n 条：空时挂起；邮箱关闭后返回 nil（丢弃剩余）。
func (m *mailbox) popBatch(n int) []envelope {
	if n <= 0 {
		return nil
	}
	select {
	case env := <-m.ch:
		batch := make([]envelope, 1, n)
		batch[0] = env
		for len(batch) < n {
			select {
			case env := <-m.ch:
				batch = append(batch, env)
			case <-m.closedCh:
				return batch
			default:
				return batch
			}
		}
		return batch
	case <-m.closedCh:
		return nil
	}
}

// close 关闭邮箱：丢弃剩余消息，并唤醒所有阻塞的 push/pop。
func (m *mailbox) close() {
	m.closeOnce.Do(func() { close(m.closedCh) })
}
