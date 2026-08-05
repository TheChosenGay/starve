package actor

import (
	"errors"
	"sync"
	"time"
)

// 请求-应答相关错误。
var (
	ErrRequestTimeout = errors.New("actor: request timeout")
	ErrDeadLetter     = errors.New("actor: dead letter")
)

// pendingRequest 是一次在途请求：回复通道 + 超时清理。
type pendingRequest struct {
	id uint64
	ch chan any
}

// Request 是一次请求的"期货"句柄（engine.Request / ctx.Request 返回）：
// 调 Wait() 阻塞等待目标回复或超时。
// 超时后引擎删除请求表项，迟到的回复直接丢弃。
// 注意：Wait 只能调用一次；同一 Request 重复调用会复用第一次的结果。
type Request struct {
	engine  *Engine
	id      uint64
	ch      chan any
	timeout time.Duration

	immediateErr error // 投递前就失败（如 dead letter），Wait 直接返回
	once         sync.Once
	value        any
	err          error
}

// Wait 阻塞直到收到回复或超时。返回 (回复内容, nil) 或 (nil, error)。
func (r *Request) Wait() (any, error) {
	r.once.Do(func() {
		if r.immediateErr != nil {
			r.err = r.immediateErr
			return
		}
		select {
		case v, ok := <-r.ch:
			if !ok {
				r.err = ErrDeadLetter
				return
			}
			r.value = v
			r.engine.cancelRequest(r.id)
		case <-time.After(r.timeout):
			r.engine.cancelRequest(r.id)
			r.err = ErrRequestTimeout
		}
	})
	return r.value, r.err
}
