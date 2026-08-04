// Package ecs 提供确定性 ECS 模拟内核（M1 实现）。
//
// 设计要点（见 docs/游戏服务器设计-Actor与ECS.md §4）：
//   - 稀疏集存储 + 泛型查询 + 惰性注册表
//   - 固定顺序系统、固定 dt、RNG/时钟收进 Resource（L1+L2 确定性）
//   - dirty 集合供快照消费
package ecs
