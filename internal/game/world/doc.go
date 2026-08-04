// Package world 实现 WorldActor：Actor 与 ECS 的接缝（M3 实现）。
//
// 设计要点（见 docs/游戏服务器设计-Actor与ECS.md §5）：
//   - 命令缓冲：玩家意图先入缓冲，tick 统一消费
//   - outbox：副作用只经 outbox 表达，由 actor 统一 drain
//   - 10Hz 固定 dt，世界时钟 = tickCount × dt
package world
