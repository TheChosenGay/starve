// Package systems 存放玩法系统（M5，AI 编写）。
//
// 约束：系统是纯函数——不调 actor API、不发网络消息，副作用只走 outbox。
package systems
