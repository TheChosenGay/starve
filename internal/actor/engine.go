package actor

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Config 是引擎配置；零值会被默认值替换。
type Config struct {
	MailboxSize     int           // 每个 process 邮箱容量，默认 1024
	BatchSize       int           // 每轮批量处理条数，默认 300
	MaxRestarts     int           // 崩溃重启上限，默认 3
	ShutdownTimeout time.Duration // Shutdown 投毒等待入队上限，默认 10s
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
	closed atomic.Bool
	cfg    Config
	logger *slog.Logger

	processes map[string]*process   // key = pid.ID（本地地址下 ID 唯一）
	byKind    map[string][]*process // kind 索引
	kindCount map[string]int        // 自动命名计数（name 为空时）

	lifecycle sync.Mutex // 保护 wg.Add 与 wg.Wait 互斥
	wg        sync.WaitGroup

	reqMu     sync.Mutex
	requests  map[uint64]*Request
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
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 10 * time.Second
	}
	return &Engine{
		cfg:       cfg,
		logger:    slog.Default(),
		processes: make(map[string]*process),
		byKind:    make(map[string][]*process),
		kindCount: make(map[string]int),
		requests:  make(map[uint64]*Request),
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
	if e.closed.Load() {
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
		kind:     kind,
		producer: producer,
		mailbox:  newMailbox(e.cfg.MailboxSize),
	}
	p.ctx = &Context{engine: e, proc: p}
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
	if err := p.send(envelope{msg: msg}); err != nil {
		e.logger.Warn("actor: dead letter", "pid", pid, "err", err)
	}
}

// ASend 异步投递消息（fire-and-forget）并限时等待入队。
// Go 没有语言级 async/await，"异步"在这里指不等待对方处理完、只保证入队；
// Send 与 ASend 都是这种异步投递，区别是：Send 在邮箱满时无限阻塞发送方，
// ASend 最多等 timeout（超时返回 ErrMailboxTimeout），避免发送方被背压卡死。
func (e *Engine) ASend(pid *PID, msg any, timeout time.Duration) error {
	p := e.lookup(pid)
	if p == nil {
		return ErrDeadLetter
	}
	if !p.startIfNeeded(e) {
		return ErrDeadLetter
	}
	return p.sendTimeout(envelope{msg: msg}, timeout)
}

// Request 请求-应答（Ask）：投递并期望目标 ctx.Respond 回复。
// 返回 *Request，调 Wait() 阻塞至回复或超时；超时后迟到回复丢弃。
// 注意：世界 actor 的 tick 内不要 Wait（纪律）。
func (e *Engine) Request(pid *PID, msg any, timeout time.Duration) *Request {
	return e.requestFrom(pid, msg, timeout, nil)
}

// requestFrom 是 Request 与 Context.Request 的共同实现：sender 标识请求方。
func (e *Engine) requestFrom(pid *PID, msg any, timeout time.Duration, sender *PID) *Request {
	p := e.lookup(pid)
	if p == nil {
		return &Request{ch: make(chan any, 1), immediateErr: ErrDeadLetter}
	}
	if !p.startIfNeeded(e) {
		return &Request{ch: make(chan any, 1), immediateErr: ErrDeadLetter}
	}
	req := e.registerRequest()
	if err := p.send(envelope{msg: msg, sender: sender, requestID: req.id}); err != nil {
		e.cancelRequest(req.id)
		return &Request{ch: make(chan any, 1), immediateErr: err}
	}
	return &Request{engine: e, id: req.id, ch: req.ch, timeout: timeout}
}

// spawnChild 在 parent 下派生子 actor：ID = parent.ID + "." + name，
// kind 继承父 actor；重复 ID panic；子 actor 记入父进程的监督列表。
func (e *Engine) spawnChild(parent *process, producer Producer, name string) *PID {
	if producer == nil {
		panic("actor: SpawnChild: nil producer")
	}
	if name == "" {
		panic("actor: SpawnChild: name required")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed.Load() {
		panic("actor: SpawnChild: engine closed")
	}
	id := parent.pid.ID + "." + name
	if _, ok := e.processes[id]; ok {
		panic(fmt.Sprintf("actor: SpawnChild: duplicate PID %q", id))
	}
	p := &process{
		pid:      PID{Address: LocalLookupAddr, ID: id},
		kind:     parent.kind,
		producer: producer,
		mailbox:  newMailbox(e.cfg.MailboxSize),
		parent:   parent,
	}
	p.ctx = &Context{engine: e, proc: p}
	e.processes[id] = p
	e.byKind[parent.kind] = append(e.byKind[parent.kind], p)
	parent.children = append(parent.children, p)
	pid := p.pid
	return &pid
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

// Poison 向指定 actor 发送毒药：actor 先处理完邮箱里已排队的消息，
// 然后给子 actor 递归投毒、最后关闭自己（之后的消息 dead letter）并退出。
// 邮箱满时阻塞发送方（背压）。
func (e *Engine) Poison(pid *PID) {
	p := e.lookup(pid)
	if p == nil {
		e.logger.Warn("actor: poison dead letter", "pid", pid)
		return
	}
	if err := p.send(envelope{msg: poisonPill}); err != nil {
		e.logger.Warn("actor: poison failed", "pid", pid, "err", err)
	}
}

// BroadcastEvent 把消息扇出给所有已注册 process（含子 actor），每人一份。
// 未启动的 process 也会被拉起（保证"所有 actor 都收到"）。
// 顺序：各邮箱各自 FIFO，无全局顺序；MVP 不做订阅过滤。
func (e *Engine) BroadcastEvent(msg any) {
	e.mu.RLock()
	procs := make([]*process, 0, len(e.processes))
	for _, p := range e.processes {
		procs = append(procs, p)
	}
	e.mu.RUnlock()
	for _, p := range procs {
		if !p.startIfNeeded(e) {
			continue // 引擎已关闭
		}
		if err := p.send(envelope{msg: msg}); err != nil {
			e.logger.Warn("actor: broadcast dead letter", "pid", p.pid, "err", err)
		}
	}
}

// Shutdown 优雅关停：只给顶层 process 投毒药；每个 process 收到毒药后先向
// 所有子 process 递归投毒、排干后自杀，因此整棵 actor 树优雅收尾。
// 投毒在 ShutdownTimeout 内入不了队（邮箱满且无消费）时强制关闭兜底。
// 幂等；关停后 Send/ASend 变为 dead letter。
// 注意：actor 不应在 Receive 内无限阻塞，否则 Shutdown 会一直等待它返回。
func (e *Engine) Shutdown() {
	e.mu.Lock()
	if e.closed.Load() {
		e.mu.Unlock()
		return
	}
	e.closed.Store(true)
	procs := make([]*process, 0, len(e.processes))
	for _, p := range e.processes {
		procs = append(procs, p)
	}
	e.mu.Unlock()

	for _, p := range procs {
		if p.parent != nil {
			continue // 子 actor 由父 process 的 onPoison 递归投毒
		}
		if err := p.sendTimeout(envelope{msg: poisonPill}, e.cfg.ShutdownTimeout); err != nil {
			p.close() // 投不进去（满/已关）→ 强制关闭兜底
		}
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

func (e *Engine) registerRequest() *Request {
	e.reqMu.Lock()
	defer e.reqMu.Unlock()
	e.nextReqID++
	r := &Request{id: e.nextReqID, ch: make(chan any, 1)}
	e.requests[r.id] = r
	return r
}

func (e *Engine) cancelRequest(id uint64) {
	e.reqMu.Lock()
	delete(e.requests, id)
	e.reqMu.Unlock()
}

// completeRequest 完成一次请求：把回复投给等待方。
// 表项已不存在（超时被清理）说明是迟到回复，直接丢弃。
func (e *Engine) completeRequest(id uint64, v any) {
	e.reqMu.Lock()
	r, ok := e.requests[id]
	if ok {
		delete(e.requests, id)
	}
	e.reqMu.Unlock()
	if !ok {
		e.logger.Warn("actor: late reply dropped", "request_id", id)
		return
	}
	select {
	case r.ch <- v:
	default:
	}
}
