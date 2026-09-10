// Package collide 是纯 Go 的三维碰撞检测几何库（服务端权威，不使用 cgo）。
//
// 设计约定：
//   - 使用 float64；右手坐标系；单位由调用方决定（本项目用米）。
//   - 本包只做几何查询：最近点、距离、重叠判定。后续再扩展射线/扫掠与宽阶段。
//   - 算法与公式来源于 Christer Ericson《Real-Time Collision Detection》第 5 章。
//
// 本包与游戏逻辑解耦：游戏层负责把整格坐标/高度场转换成世界坐标再调用。
package collide
