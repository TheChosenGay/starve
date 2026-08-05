package actor

import (
	"errors"
	"sync"
)

// 邮箱相关错误。
var (
	ErrMailboxClosed = errors.New("actor: mailbox closed")
)

// envelope 是邮箱里的一条消息：业务消息 + 发送者 + 可选的请求上下文。
// requestID 为 0 表示普通消息，非 0 表示这是一次请求（ASend），目标 actor
// 处理完用 ctx.Respond 回复时靠它路由。
type envelope struct {
	msg       any
	sender    *PID
	requestID uint64
}

// mailbox 是有界 ringbuffer 邮箱：actor 的收件箱（inbox）。
//
// 并发模型：N 个发送者 push、1 个处理 goroutine popBatch，由一把锁 + 条件
// 变量保护。空时 pop 挂起，满时 push 阻塞（背压），close 广播唤醒双方。
type mailbox struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []envelope
	head   int
	count  int
	closed bool
}

func newMailbox(capacity int) *mailbox {
	if capacity <= 0 {
		capacity = 1
	}
	m := &mailbox{buf: make([]envelope, capacity)}
	m.cond = sync.NewCond(&m.mu)
	return m
}

// push 投递一条消息：满时阻塞（背压），邮箱关闭后返回 ErrMailboxClosed。
func (m *mailbox) push(env envelope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.count == len(m.buf) && !m.closed {
		m.cond.Wait()
	}
	if m.closed {
		return ErrMailboxClosed
	}
	m.buf[(m.head+m.count)%len(m.buf)] = env
	m.count++
	m.cond.Broadcast() // 唤醒可能阻塞的 pop
	return nil
}

// popBatch 一次取出最多 n 条：空时挂起；邮箱关闭后返回 nil。
func (m *mailbox) popBatch(n int) []envelope {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.count == 0 && !m.closed {
		m.cond.Wait()
	}
	if m.closed {
		return nil
	}
	if n <= 0 || n > m.count {
		n = m.count
	}
	batch := make([]envelope, n)
	for i := 0; i < n; i++ {
		batch[i] = m.buf[m.head]
		m.head = (m.head + 1) % len(m.buf)
	}
	m.count -= n
	m.cond.Broadcast() // 唤醒可能阻塞的 push
	return batch
}

// close 关闭邮箱：丢弃剩余消息，并唤醒所有阻塞的 push/pop。
func (m *mailbox) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		m.cond.Broadcast()
	}
}
