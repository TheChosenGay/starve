# starve

饥荒（Don't Starve）类生存游戏的服务器端项目。

核心四件套（Actor / ECS / Gateway / Cluster）全部自研，其余（玩法、存档、客户端工具等）由 AI 基于核心接口实现。

## 文档

- 设计文档：[docs/游戏服务器设计-Actor与ECS.md](docs/游戏服务器设计-Actor与ECS.md)
- 规划方案：[docs/项目规划方案.md](docs/项目规划方案.md)
- P0.1 真实基线与架构决策：[docs/P0.1-真实基线与架构决策.md](docs/P0.1-真实基线与架构决策.md)
- P0.2 质量门禁与可观测性：[docs/P0.2-质量门禁与可观测性.md](docs/P0.2-质量门禁与可观测性.md)
- P0.3 服务端可观测性：[docs/P0.3-可观测性.md](docs/P0.3-可观测性.md)
- P1.1 移动预测协议契约：[docs/P1.1-移动预测协议契约.md](docs/P1.1-移动预测协议契约.md)
- P1.3 输入流（序号消费与多步追赶）：[docs/P1.3-输入流-序号消费与多步追赶.md](docs/P1.3-输入流-序号消费与多步追赶.md)
- P1.4 跨端一致性语料（移动/碰撞/ORCA）：[docs/P1.4-跨端一致性语料.md](docs/P1.4-跨端一致性语料.md)
  - ⚠️ 其中 §11「移动相关组件必须每 tick 标脏」是**硬规则**（踩过两次：走动一卡一跳、炸弹一格一顿）
- P1.2 权威动作状态机设计：[docs/P1.2-权威动作状态机.canvas.tsx](docs/P1.2-权威动作状态机.canvas.tsx)
- 花与灌木 Godot 客户端适配：[docs/花与灌木-Godot客户端适配指南.md](docs/花与灌木-Godot客户端适配指南.md)
- 占位物与墙角滑动 Godot 客户端适配：[docs/占位物与墙角滑动-Godot客户端适配指南.md](docs/占位物与墙角滑动-Godot客户端适配指南.md)
- 模型 → 服务端碰撞体流水线：[docs/模型到碰撞体流水线.md](docs/模型到碰撞体流水线.md)
- 渲染资产仓（独立仓库）：[TheChosenGay/asset-starve](https://github.com/TheChosenGay/asset-starve)
- 终端客户端（不开 Godot 测服务端）：[docs/终端客户端-TUI.md](docs/终端客户端-TUI.md)
- Gateway 复用评估：[docs/gateway-comet复用评估.md](docs/gateway-comet复用评估.md)
- M4 网关实现设计：[docs/M4网关实现设计.md](docs/M4网关实现设计.md)
- comet 机制详解：[docs/comet机制详解.md](docs/comet机制详解.md)

## 状态

M0～M5 已完成，当前已有 Godot 客户端和移动、采集、战斗、背包、制作、建造、昼夜、天气、存档/回放闭环；Cluster 仍未启动。当前可运行事实、边界和验收命令以 [P0.1 真实基线](docs/P0.1-真实基线与架构决策.md) 为准，历史演进详见[项目规划方案](docs/项目规划方案.md)。

`world.player.automate` 的 `ANY` 模式保持空格现有语义；`ATTACK_ONLY` 供 F 键使用，目标选择与攻击判定均由服务端完成。

动作结果统一通过 `SnapshotDelta.events[].outcome` 与组件变更同 tick 下发；
`world.action.outcome` 独立推送已 deprecated，仅保留旧客户端协议兼容。攻击时序为
400ms windup + 400ms recovery（20Hz 下各 8 tick），命中发生在 commit tick。

完整本地质量门禁：

```bash
make check
```

本地 Grafana 看板：`make run-gate-observe` 后执行 `make observe`，打开 http://localhost:3000/d/starve-server 。说明见 [P0.3 可观测性](docs/P0.3-可观测性.md)。
