# Boss 接入清单（模型就位后照着做）

> 上游：`行为树-设计与接入指南.md`（§9.0 Boss 意图 → 世界层消费）、
> `模型到碰撞体流水线.md`（body 尺寸怎么来的）。
>
> 一句话：**服务端逻辑与协议接口已经就绪，剩下的是"模型 + 数值 + 生成点"三件事**。
> 本文件就是那三件事的勾选表。

---

## 0. 现在已经就绪的（不用再动）

| 已经就绪 | 在哪 |
| --- | --- |
| Boss 生物类型 `CREATURE_KIND_BOSS = 8`（双端枚举 + 客户端已能识别/命名） | `pkg/proto/game/game.proto`、`components.CreatureKindByName["boss"]` |
| Boss 行为树（阶段一投弹 / 阶段二嚎叫 + 闪现 + 三拳一砸） | `internal/game/behavior/boss_tree.go` |
| 技能真实效果：投弹（真炸弹 + 抛物线 + 落地爆炸）、锤地 AOE（伤害 + 击退 + 广播）、闪现（落点校验后瞬移） | `internal/game/world/boss_action.go`（`BossActionSystem`） |
| 技能**表现/复制接口**：4 个技能起手都会起一个带 windup 的权威动作 | proto `ACTION_KIND_BOSS_THROW/LEAP/SLAM/ROAR`、`systems/boss_windup.go` |
| 客户端模型目录 / 片段映射 / 名字（含模型缺失时的占位体） | `GodotClient/Game/ActorCatalog3D.cs`、`RiggedActor3D.cs`、`GameRoot.cs` |
| 生物模板 `boss`（占位数值，见下） | `configs/creatures.json` |

**当前刻意没做**：没有任何生成规则引用 `boss` ⇒ 地图生成与既有存档**完全不变**，
Boss 不会自己出现（见 §3）。

---

## 1. 模型与碰撞尺寸（必做）

1. 模型放进 `configs/models.json`（照既有生物条目的写法，注明 `kind: creature`）；
2. 跑 `go run ./cmd/modelcollide` 推导 body 尺寸；
3. 把推导结果写回 `configs/creatures.json` 的 `body_radius / body_height / body_half_length`。

⚠️ 现在这三项是**占位值**（0.9 / 2.4 / 0），只是为了配置能通过校验；
它们决定命中判定与占位，必须由模型推导，不要手写整数凑。

客户端模型路径约定：`res://assets/models/boss/boss.glb`
（常量 `ActorCatalog3D.BossModelPath`）。**模型不存在时会看到一个占位体**，
所以现在就能进场景观察行为，不需要等美术。

## 2. 动画片段（必做）

模型需要提供（缺哪个就用下面的回退族，不会崩，只是动作不对）：

| 动作 | clip 名（首选 → 回退） | 触发来源 |
| --- | --- | --- |
| 待机 / 移动 | `idle` / `walk` | 通用 locomotion |
| 出拳 | `punch` → `hook` → `attack` → `proc_attack` | `ACTION_KIND_ATTACK` |
| 投弹 | 同出拳族 | `ACTION_KIND_BOSS_THROW`（起手 12 tick） |
| 闪现 | `leap` → `jump` → `dash` → `attack` | `ACTION_KIND_BOSS_LEAP`（8 tick，位移是瞬时的） |
| 锤地 | `slam` → `smash` → `attack` | `ACTION_KIND_BOSS_SLAM`（**起手 20 tick**，动作结束那一刻正好是 AOE 打出） |
| 嚎叫 | `roar` → `howl` → `cast` → `idle` | `ACTION_KIND_BOSS_ROAR`（**30 tick**） |
| 受击 / 死亡 | `hit` / `death` | 通用受击链路 |

改 clip 名只改 `RiggedActor3D.PlayAction` 里那一个 switch（文件注释已标明）。

## 3. 生成点（需要你拍板）

Boss 目前**没有**任何生成规则。三种可选做法，各有代价：

| 做法 | 怎么改 | 代价 |
| --- | --- | --- |
| 手摆（推荐先用这个） | `configs/map.json` 加 `creatures: [{kind: "boss", x, y}]` | 地图生成是**确定性 RNG**：改动会让既有地图/存档的布局变化 |
| 区域/群系掉落 | `configs/biomes.json` 某群系加 `category: "creature"`、`source: "boss"` 的 drop | 同上；且要有稀有度控制，否则 Boss 会像野怪一样刷 |
| 专门的首领房 | 新区域规则 + 生成器支持 | 工作量最大，但最可控 |

⚠️ 只要一改生成规则，**既有地图与新地图就不一致**（同 seed 也会变）。
建议：先用「手摆」在一个新 seed 上验证，确认后再决定上正式生成规则。

## 4. 数值待定（配置里现在是占位）

`configs/creatures.json` 的 `boss` 条目：`hp 400`、`attack_damage 4`、
`attack_cooldown 24`、`throw_strength 40`（≈ 17 格投掷距离）、`leash 24`、
`roam_radius 0`（守家）。阶段阈值不在配置里，在
`behavior.DefaultBossConfig()`（`Phase2HP 200 / SlamTicks 20 / RoarTicks 30 /
ThrowIntervalTicks 20`）—— 调这些会同时改客户端动画时长（动作时长就取这些值）。

## 5. 还可能想补的表现（目前只有动作与爆炸）

| 表现 | 现状 | 要补什么 |
| --- | --- | --- |
| 锤地扩散圈 / 震屏 | **已有**：服务端广播 `BlastEvent`（`thrown_entity = 0`），客户端 `BlastFxLayer3D` 在画 | — |
| 投弹飞行 | **已有**：`Thrown` 逐 tick 复制 + 客户端抛物线预测/插值 | — |
| 闪现残影 / 尘土 | 无 | 位置跳变没有事件；要么加一个 Boss 事件，要么客户端按 `BOSS_LEAP` 动作自己起特效 |
| 嚎叫音波 / 阶段变化提示 | 无（阶段只体现在 `AI.Phase2HP` 的行为切换） | 同上：按 `BOSS_ROAR` 动作起表现即可，不必新增协议 |
| 血条/Boss 名 | 通用生物名与血条已生效（名字「首领」是占位） | 首领血条 UI（如果需要专属样式） |
