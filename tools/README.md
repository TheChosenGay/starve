# tools

## pomelo-client（M4 测试客户端）

命令行 pomelo 客户端：连接 → 握手 → 登录 → 移动 → 打印位置推送。

```bash
# 启动网关（另一个终端）
go run ./cmd/gate

# 客户端 A：只收推送
go run ./tools/pomelo-client -uid 42 -duration 15s

# 客户端 B：每 500ms 向右移动一步
go run ./tools/pomelo-client -uid 43 -move 1,0 -interval 500ms -duration 15s
```

### 双客户端验收

1. 客户端 A 登录后应看到 `entity=2`（B 的实体）的位置推送；
2. 客户端 B 移动后，A 的推送中 `实体 2` 的坐标应随 `(1,0)` 递增；
3. A 自己的移动（若开启）也会广播回 A 自己（`实体 1`）。

参数：`-addr`（默认 `ws://localhost:8081/ws`）、`-uid`（token = `u<uid>`）、
`-move "dx,dy"`（可选）、`-interval`（移动间隔）、`-duration`（运行时长）。
