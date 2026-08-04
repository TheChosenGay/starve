// Package gateway 提供客户端接入层（M4 实现）。
//
// 设计要点（见 docs/游戏服务器设计-Actor与ECS.md §6）：
//   - pomelo 协议（握手/心跳/route/request/notify/response/push）
//   - agent actor：单写者写队列 + 生命周期状态机
//   - 路由注册表 route → 消息类型 → 目标 PID
//   - 会话绑定 UID ↔ PID
//
// 实现蓝本：feeds 的 comet 连接层骨架（见 docs/gateway-comet复用评估.md）。
package gateway
