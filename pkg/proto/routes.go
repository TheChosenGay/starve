package proto

// 路由常量：客户端/服务端共享的 pomelo route 契约（见 message.proto）。
const (
	RouteLogin = "gate.login"
	RouteMove  = "world.player.move"
	// 推送路由（服务端 → 客户端）
	RouteSnapshot      = "world.snapshot"
	RouteSnapshotDelta = "world.snapshot.delta"
)
