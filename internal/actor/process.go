package actor

import "sync"

// process 是 actor 的运行时外壳：PID + Producer + 邮箱 + 重启计数。
// Spawn 时创建，Stop 时销毁；崩溃重启只替换 actor 实例，process/PID 不变。
type process struct {
	pid      PID
	producer Producer
	mailbox  *mailbox

	startOnce sync.Once // 保证处理 goroutine 只启动一次（按需）
	actor     Actor     // 惰性：首次交付消息前由 producer.Produce() 创建
	restarts  int       // 崩溃重启计数（MaxRestarts 上限，下一步用）
}

// startIfNeeded 在第一条消息到达时启动处理 goroutine。
// 引擎已关闭时返回 false（消息变为 dead letter）。
func (p *process) startIfNeeded(e *Engine) bool {
	started := false
	p.startOnce.Do(func() {
		e.lifecycle.Lock()
		defer e.lifecycle.Unlock()
		if e.closed {
			return
		}
		e.wg.Add(1)
		started = true
		go p.run(e)
	})
	return started
}

// run 是处理循环：批量取出消息并交付给 actor。
//
// TODO(M2 下一步)：Actor.Receive 契约确定后，在这里为每条消息设置
// Context（sender/requestID），调用 p.actor.Receive(ctx)，并处理
// panic → 崩溃缓冲 → 重启（MaxRestarts）。当前先占位消费，保证
// Send 不阻塞、邮箱语义可测。
func (p *process) run(e *Engine) {
	defer e.wg.Done()
	for {
		batch := p.mailbox.popBatch(e.cfg.BatchSize)
		if batch == nil {
			return // 邮箱关闭
		}
		for _, env := range batch {
			_ = env // TODO: 交付给 actor
		}
	}
}
