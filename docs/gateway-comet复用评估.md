# Gateway 复用评估：~/feeds 的 comet module

> 状态：评估完成（2026-08-04）
> 结论：**不整体依赖 comet**；取其连接层骨架（单写者写泵 + 连接管理 + 协议无关 Core）作为 starve `internal/gateway` 的实现蓝本，协议层替换为 pomelo。

## 1. comet 是什么

feeds 的长连接实时通信模块（`~/feeds/pkg/comet`，核心代码约 800 行）：

- 协议：自定义帧 `[2B type][payload]`，WebSocket 承载
  - `0x0001` Heartbeat / `0x0002` Auth / `0x0003` Message；心跳回包 `0x0000`
- 分层：
  - `Core`：协议无关 dispatch（auth / heartbeat / message / push）
  - `ConnManager`：线程安全连接注册表 + 房间绑定 + 房间广播
  - `Business` 接口：`OnAuth` / `OnMessage`，业务层实现
  - `ws.Server` / `ws.Conn`：WS 实现（读循环 + 单写者写泵）
  - `client.Conn`：客户端/服务端间连接，自动鉴权握手 + 心跳
- 生产形态：`services/live` 独立进程，WS(8081) + gRPC LiveService(9006)，gRPC 暴露 PushRoom / PushUser / IsOnline

## 2. 与 starve Gateway 设计的差距

| 需求（设计文档 §6） | comet 现状 | 差距 |
|------|------------|------|
| pomelo 协议（握手/心跳/route/request/notify/response/push） | 自定义 2 字节帧协议 | **协议不兼容**，Unity/Cocos/微信等现成 SDK 无法直连 |
| agent actor（每连接一个，状态机 Init→WaitAck→Working→Closed） | 每连接一个 ws.Conn，无状态机、无 actor | 缺状态机 / 踢线 / 唯一关闭路径 |
| 路由注册表 route → 消息类型 → 目标 PID | 无路由概念，`OnMessage` 收裸字节 | 需新增 |
| mid 关联（request/response 不维护 pending 表） | 无请求响应语义 | 需新增 |
| UID ↔ PID 会话绑定 | `ConnManager` 只做 UID ↔ conn（roomID=userID） | 需扩展为 UID ↔ agent PID |
| 断线重连 / 离线保护 | 连接关了就删 | 需接入世界 actor |

依赖层面：comet 不是独立 module，是 `github.com/TheChosenGay/feeds/pkg` 的一部分；直接 import 该 module 会把 grpc / otel / kafka / redis 等一整套依赖带进来。而 comet 自身真正用到的只有 `gorilla/websocket` + `google/uuid`（otel 可选），拷贝成本很低。

## 3. 值得直接搬用的部分（蓝本）

1. `ws.Conn` 的单写者模型：读写分离，WritePump 独占写 socket —— 正好是设计文档 §6.2 第一条铁律，可直接照搬；
2. `ConnManager`：线程安全连接表 + 房间绑定 + 广播，作为会话/连接表的基础（补上 UID ↔ PID 语义）；
3. `Core` 的协议无关 dispatch 形态：把 `[2B type]` 分发换成 pomelo package/message 分发，结构不变；
4. `Business` 接口：`OnAuth` → 登录校验，`OnMessage` → 路由注册表解析 + 投递 world actor。

## 4. 结论与落地方式

- 不把 feeds/comet 作为依赖 import（协议不兼容 + 拖入整模块依赖）；
- M4 实现 Gateway 时，以 comet 的 ws.Conn / ConnManager / Core 为骨架写进 `internal/gateway`，协议层用 pomelo codec（蓝本 cherry `net/parser/pomelo`），会话层补 agent 状态机 + 路由注册表 + UID ↔ PID；
- 改动可控、保留成熟设计，且不偏离设计文档已锁定的 pomelo 决策。
