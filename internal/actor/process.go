package actor

import (
	"sync"
	"sync/atomic"
)

// process 是 actor 的运行时外壳：PID + Producer + 邮箱 + 重启计数 + 子 actor。
// Spawn 时创建，永久停止时销毁；崩溃重启只替换 actor 实例，process/PID 不变。
type process struct {
	pid      PID
	kind     string
	producer Producer
	mailbox  *mailbox
	ctx      *Context

	startMu  sync.Mutex  // 首次启动临界区（与生命周期锁配合，保证只启动一次）
	started  atomic.Bool // 处理 goroutine 是否已启动（按需）；被所有发送方并发读
	actor    IActor      // 惰性：首次交付消息前调用 producer() 创建
	restarts int         // 崩溃重启计数（MaxRestarts 上限）
	dead     bool        // 超过重启上限，永久停止
	children []*process
}

// startIfNeeded 在第一条消息到达时启动处理 goroutine；
// 已启动则直接返回 true。引擎已关闭（且尚未启动过）时返回 false。
//
// started 会被任意数量的发送方 goroutine 并发访问（每个 Send 都走这里），
// 所以用 atomic.Bool 做无锁快路径；只有首次启动才进 startMu 临界区，
// 并在 lifecycle 锁下 wg.Add——保证与 Shutdown 的 wg.Wait 互斥。
func (p *process) startIfNeeded(e *Engine) bool {
	if p.started.Load() {
		return true
	}
	p.startMu.Lock()
	defer p.startMu.Unlock()
	if p.started.Load() {
		return true
	}
	e.lifecycle.Lock()
	defer e.lifecycle.Unlock()
	if e.closed.Load() {
		return false
	}
	e.wg.Add(1)
	p.started.Store(true)
	go p.run(e)
	return true
}

// run 是处理循环：批量取出消息并逐条交付。
func (p *process) run(e *Engine) {
	defer e.wg.Done()
	for {
		batch := p.mailbox.popBatch(e.cfg.BatchSize)
		if batch == nil {
			return // 邮箱关闭
		}
		if !p.deliverBatch(e, batch) {
			return // 永久停止
		}
	}
}

// deliverBatch 交付一批消息。panic 时重启实例并继续交付本批剩余消息
// （等价于崩溃缓冲：已取出未交付的消息不丢）。
// 返回 false 表示超过 MaxRestarts，actor 永久停止。
func (p *process) deliverBatch(e *Engine, batch []envelope) bool {
	for _, env := range batch {
		p.deliverOne(e, env)
		if p.dead {
			return false
		}
	}
	return true
}

func (p *process) deliverOne(e *Engine, env envelope) {
	defer func() {
		if r := recover(); r != nil {
			e.logger.Error("actor panicked", "pid", p.pid, "panic", r)
			p.restarts++
			if p.restarts > e.cfg.MaxRestarts {
				e.logger.Error("actor exceeded max restarts, stopping", "pid", p.pid)
				p.dead = true
				p.stopRecursive()
				return
			}
			p.restart(e)
		}
	}()
	p.ensureActor(e)
	p.ctx.env = env
	p.actor.Receive(p.ctx)
}

// restart 重建 actor 实例（PID/邮箱/子 actor 不变）。
func (p *process) restart(e *Engine) {
	defer func() {
		if r := recover(); r != nil {
			// 连重启（Produce）都失败：直接永久停止
			e.logger.Error("actor restart failed, stopping", "pid", p.pid, "panic", r)
			p.dead = true
			p.stopRecursive()
		}
	}()
	p.actor = nil
	p.ensureActor(e)
	e.logger.Warn("actor restarted", "pid", p.pid, "restarts", p.restarts)
}

// ensureActor 惰性创建 actor 实例（首次交付前）。
func (p *process) ensureActor(e *Engine) {
	if p.actor == nil {
		p.actor = p.producer()
	}
}

// stopRecursive 关闭自己与全部子 actor 的邮箱（永久停止时调用）。
func (p *process) stopRecursive() {
	p.mailbox.close()
	for _, c := range p.children {
		c.stopRecursive()
	}
}
