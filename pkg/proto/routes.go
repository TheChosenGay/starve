package proto

// 路由常量：客户端/服务端共享的 pomelo route 契约。
// 关联方式：
//   - 输入路由（客户端→服务器，如 RouteLogin / RouteMove）由网关 Router
//     注册表解析：route → proto 消息类型 + 目标，反序列化后投递；
//   - 输出路由（服务器→客户端，如 RouteSnapshot / RouteSnapshotDelta）
//     在网关组装 pomelo push 时直接带上，客户端按 route 分发到对应
//     proto 消息解析（见 pkg/proto/message.proto 与 game.proto）。
const (
	RouteLogin  = "gate.login"
	RouteMove   = "world.player.move"
	RouteGather = "world.player.gather"
	RouteAttack = "world.player.attack"
	RoutePickup = "world.player.pickup"
	RouteUse    = "world.player.use"
	RouteEquip  = "world.player.equip"
	RouteChop   = "world.player.chop"
	RouteMine   = "world.player.mine"
	RouteDrop   = "world.player.drop"
	RouteSave   = "game.save" // 客户端点存档
	RouteCraft  = "world.player.craft" // request/response
	RouteCancelCraft = "world.player.craft.cancel" // notify
	RouteSplit       = "world.player.split"        // notify
	RouteBuild       = "world.build"               // request（创建未放置建筑，响应 BuildResponse）
	RoutePlace       = "world.place"               // notify（放置建筑）
	RouteBuildCheck  = "world.build.check"         // request（可放置查询）
	RouteDemolish    = "world.demolish"            // notify
	// 推送路由（服务端 → 客户端）
	RouteSnapshot      = "world.snapshot"
	RouteSnapshotDelta = "world.snapshot.delta"
	RouteCraftDone     = "world.craft.done"
	RouteConfig        = "world.config"
	RouteWeatherFrame  = "world.weather.frame"
)
