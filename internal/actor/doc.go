// Package actor 提供 hollywood 风格的 actor 运行时（M2 实现）。
//
// 设计要点（见 docs/游戏服务器设计-Actor与ECS.md §3）：
//   - ringbuffer 邮箱 + 按需 goroutine + 批处理
//   - 类型化消息分发（无反射）
//   - 崩溃缓冲 + 自动重启 + 子 actor 监督
//   - PID 寻址、kind 激活、Request/Respond
//
// 当前进度：Actor.Receive(msg) 契约已定（可选 SetContext 注入环境）；
// Engine 已支持 Spawn/Send/ASend/Shutdown/GetPid/GetPids、ringbuffer 邮箱、
// 消息交付、Context/Respond 请求应答、崩溃缓冲 + 自动重启 + 子 actor 监督。
// 待办：SendRepeat 定时器、BroadcastEvent 广播。
package actor
