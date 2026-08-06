package proto

// 路由常量：客户端/服务端共享的 pomelo route 契约。
// 关联方式：
//   - 输入路由（客户端→服务器，如 RouteLogin / RouteMove）由网关 Router
//     注册表解析：route → proto 消息类型 + 目标，反序列化后投递；
//   - 输出路由（服务器→客户端，如 RouteSnapshot / RouteSnapshotDelta）
//     在网关组装 pomelo push 时直接带上，客户端按 route 分发到对应
//     proto 消息解析（见 pkg/proto/message.proto 与 game.proto）。
const (
	RouteLogin = "gate.login"
	RouteMove  = "world.player.move"
	// 推送路由（服务端 → 客户端）
	RouteSnapshot      = "world.snapshot"
	RouteSnapshotDelta = "world.snapshot.delta"
)
