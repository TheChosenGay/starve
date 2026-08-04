// Package actor 提供 hollywood 风格的 actor 运行时（M2 实现）。
//
// 设计要点（见 docs/游戏服务器设计-Actor与ECS.md §3）：
//   - ringbuffer 邮箱 + 按需 goroutine + 批处理
//   - 类型化消息分发（无反射）
//   - 崩溃缓冲 + 自动重启 + 子 actor 监督
//   - PID 寻址、kind 激活、Request/Respond
package actor
