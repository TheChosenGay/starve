package gateway

import "sync"

// TargetKind 路由目标类型。
type TargetKind uint8

const (
	TargetWorld TargetKind = iota // 世界 actor（MVP 单世界）
	TargetAgent                   // 当前连接自身
)

// RouteEntry 一条路由：反序列化目标 + 目标规则。
type RouteEntry struct {
	MsgType any // 反序列化目标类型，如 (*proto.PlayerMove)(nil)
	Target  TargetKind
}

// Router 路由注册表：route 字符串 → 消息类型 + 目标（设计文档 §6.3）。
// 网关用它做反序列化决策；业务 actor 不感知 pomelo route。
type Router struct {
	mu      sync.RWMutex
	entries map[string]RouteEntry
}

func NewRouter() *Router {
	return &Router{entries: make(map[string]RouteEntry)}
}

// Register 注册一条路由（重复注册直接覆盖）。
func (r *Router) Register(route string, entry RouteEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[route] = entry
}

// Resolve 解析路由。
func (r *Router) Resolve(route string) (RouteEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[route]
	return e, ok
}
