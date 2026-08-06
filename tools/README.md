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

## pomelo-stress（多用户压力测试）

多个客户端并发登录、频繁复杂操作（每批多条移动），统计操作端到端延迟、验证互见。

```bash
go run ./tools/pomelo-stress -clients 5 -duration 10s -interval 100ms -moves 3
```

输出：每客户端 `sent/updates/延迟(min/avg/max/p95)/unmatched` + 全体统计 + 互见验证。
`unmatched` 应接近 0（操作未被确认的条数）；延迟约 100~150ms（含最多一个 tick 等待）。
