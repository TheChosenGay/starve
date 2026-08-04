// Package ecs 提供确定性 ECS 模拟内核（M1 实现）。
//
// 设计要点（见 docs/游戏服务器设计-Actor与ECS.md §4）：
//   - 稀疏集存储 + 泛型查询 + 惰性注册表
//   - 固定顺序系统、固定 dt、RNG/时钟收进 Resource（L1+L2 确定性）
//   - dirty 集合供快照消费
//
// API 形态：Go 不支持泛型方法，设计草案中的 w.Add[T](...) 以包级泛型函数
// 实现：Add[T](w, e, c) / Get[T](w, e) / Query[T](w, fn) 等。
package ecs
