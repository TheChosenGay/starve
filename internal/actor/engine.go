package actor

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Config 是引擎配置；零值会被默认值替换。
type Config struct {
	MailboxSize int // 每个 actor 邮箱容量，默认 1024
	BatchSize   int // 每轮批量处理条数，默认 300
	MaxRestarts int // 崩溃重启上限，默认 3
}

// Engine 是 actor 运行时：管理本地注册表（PID → process）、
// 请求应答注册表、进程生命周期与关停。
//
// 并发：注册表一把 RWMutex；每个 process 的邮箱各自一把锁；
// lifecycle 锁保护 wg.Add 与 Shutdown 的 wg.Wait 互斥，避免 WaitGroup 误用。
// 当前只实现本地寻址（PID.Address == LocalLookupAddr）；远程地址
// （M6 Cluster）投递层在这里扩展。
type Engine struct {
	mu     sync.RWMutex
	closed bool
	cfg    Config
	logger *slog.Logger

	processes map[string]*process   // key = pid.ID（本地地址下 ID 唯一）
	byKind    map[string][]*process // kind 索引
	kindCount map[string]int        // 自动命名计数（name 为空时）

	lifecycle sync.Mutex // 保护 wg.Add 与 wg.Wait 互斥
	wg        sync.WaitGroup

	reqMu     sync.Mutex
	requests  map[uint64]*pendingRequest
	nextReqID uint64
}

// NewEngine 创建引擎。传入零值 Config 使用默认参数。
func NewEngine(cfg Config) *Engine {
	if cfg.MailboxSize <= 0 {
		cfg.MailboxSize = 1024
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 300
	}
	if cfg.MaxRestarts <= 0 {
		cfg.MaxRestarts = 3
	}
	return &Engine{
		cfg:       cfg,
		logger:    slog.Default(),
		processes: make(map[string]*process),
		byKind:    make(map[string][]*process),
		kindCount: make(map[string]int),
		requests:  make(map[uint64]*pendingRequest),
	}
}

// Spawn 注册一个新 actor：分配 PID（kind/name，name 为空则自动编号），
// 不启动 goroutine（惰性）。同 kind+name 重复注册会 panic。
func (e *Engine) Spawn(producer Producer, kind, name string) *PID {
	if producer == nil {
		panic("actor: Spawn: nil producer")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		panic("actor: Spawn: engine closed")
	}
	if name == "" {
		e.kindCount[kind]++
		name = fmt.Sprintf("%d", e.kindCount[kind])
	}
	id := kind + "/" + name
	if _, ok := e.processes[id]; ok {
		panic(fmt.Sprintf("actor: Spawn: duplicate PID %q", id))
	}
	p := &process{
		pid:      PID{Address: LocalLookupAddr, ID: id},
		producer: producer,
		mailbox:  newMailbox(e.cfg.MailboxSize),
	}
	e.processes[id] = p
	e.byKind[kind] = append(e.byKind[kind], p)
	pid := p.pid
	return &pid
}

// Send 异步投递消息（fire-and-forget），立即返回，无确认。
// 目标不存在或引擎已关闭 → dead letter（记日志丢弃）。
// 邮箱满时阻塞发送方（背压）。
func (e *Engine) Send(pid *PID, msg any) {
	p := e.lookup(pid)
	if p == nil {
		e.logger.Warn("actor: dead letter", "pid", pid)
		return
	}
	if !p.startIfNeeded(e) {
		e.logger.Warn("actor: dead letter (engine closed)", "pid", pid)
		return
	}
	if err := p.mailbox.push(envelope{msg: msg}); err != nil {
		e.logger.Warn("actor: dead letter", "pid", pid, "err", err)
	}
}

// ASend 请求-应答（Ask）：带超时投递并返回 *Response，等目标回复。
// 目标回复走 ctx.Respond（下一步实现）；超时后迟到回复丢弃。
// 注意：调 Wait() 才阻塞；世界 actor 的 tick 内不要 Wait（纪律）。
func (e *Engine) ASend(pid *PID, msg any, timeout time.Duration) *Response {
	p := e.lookup(pid)
	if p == nil {
		return &Response{ch: make(chan any, 1), immediateErr: ErrDeadLetter}
	}
	if !p.startIfNeeded(e) {
		return &Response{ch: make(chan any, 1), immediateErr: ErrDeadLetter}
	}
	req := e.registerRequest()
	if err := p.mailbox.push(envelope{msg: msg, requestID: req.id}); err != nil {
		e.cancelRequest(req.id)
		return &Response{ch: make(chan any, 1), immediateErr: err}
	}
	return &Response{engine: e, id: req.id, ch: req.ch, timeout: timeout}
}

// GetPid 按 kind + name 精确查找（name 为空返回 false）。
func (e *Engine) GetPid(kind, name string) (*PID, bool) {
	if name == "" {
		return nil, false
	}
	e.mu.RLock()
	p, ok := e.processes[kind+"/"+name]
	e.mu.RUnlock()
	if !ok {
		return nil, false
	}
	pid := p.pid
	return &pid, true
}

// GetPids 返回某 kind 的全部 PID，按 ID 排序（确定性）。
func (e *Engine) GetPids(kind string) []*PID {
	e.mu.RLock()
	procs := e.byKind[kind]
	out := make([]*PID, 0, len(procs))
	for _, p := range procs {
		pid := p.pid
		out = append(out, &pid)
	}
	e.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Shutdown 优雅关停：关闭所有邮箱（唤醒阻塞的 push/pop）、
// 等处理 goroutine 退出。幂等；关停后 Send/ASend 变为 dead letter。
func (e *Engine) Shutdown() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	procs := make([]*process, 0, len(e.processes))
	for _, p := range e.processes {
		procs = append(procs, p)
	}
	e.mu.Unlock()

	for _, p := range procs {
		p.mailbox.close()
	}
	e.lifecycle.Lock()
	e.wg.Wait()
	e.lifecycle.Unlock()
}

// lookup 在本地注册表查找 process；远程地址或未知 PID 返回 nil。
func (e *Engine) lookup(pid *PID) *process {
	if pid == nil || pid.Address != LocalLookupAddr {
		return nil
	}
	e.mu.RLock()
	p := e.processes[pid.ID]
	e.mu.RUnlock()
	return p
}

func (e *Engine) registerRequest() *pendingRequest {
	e.reqMu.Lock()
	defer e.reqMu.Unlock()
	e.nextReqID++
	pr := &pendingRequest{id: e.nextReqID, ch: make(chan any, 1)}
	e.requests[pr.id] = pr
	return pr
}

func (e *Engine) cancelRequest(id uint64) {
	e.reqMu.Lock()
	delete(e.requests, id)
	e.reqMu.Unlock()
}
