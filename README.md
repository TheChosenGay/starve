# starve

饥荒（Don't Starve）类生存游戏的服务器端项目。

核心四件套（Actor / ECS / Gateway / Cluster）全部自研，其余（玩法、存档、客户端工具等）由 AI 基于核心接口实现。

## 文档

- 设计文档：[docs/游戏服务器设计-Actor与ECS.md](docs/游戏服务器设计-Actor与ECS.md)
- 规划方案：[docs/项目规划方案.md](docs/项目规划方案.md)
- Gateway 复用评估：[docs/gateway-comet复用评估.md](docs/gateway-comet复用评估.md)

## 状态

M0 工程骨架、M1（ECS 内核）、M2（Actor 内核）、M3（Actor↔ECS 接缝）已完成；M4（Gateway 最小闭环）待启动。详见[项目规划方案](docs/项目规划方案.md)。
