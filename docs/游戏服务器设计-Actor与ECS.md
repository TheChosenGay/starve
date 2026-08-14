# 游戏服务器核心设计（Actor / ECS / Gateway）

> 状态：方案讨论稿 v0.5
> 目标：自研核心四件套（Actor、ECS、Cluster、Gateway），其余（玩法、存档、客户端工具等）由 AI 基于核心接口实现。
> 本文档记录 Actor、ECS、Gateway 的完整讨论：边界、选型理由、设计决策、原理详解与接口契约。

## 1. 总体层次

```text
Gateway(连接/协议)        Cluster(跨节点寻址/RPC)
        \                    /
         \                  /
          Actor(并发与所有权边界)
                   |
                ECS(模拟内核)
```

依赖单向：ECS 是最底层模拟内核（纯数据 + 逻辑）；Actor 持有 ECS（一个世界/房间 = 一个 actor + 一个 ECS World）；Gateway 与 Cluster 只通过 Actor 寻址、投递消息，不直接碰 ECS。

| 模块 | 边界 | 管什么 | 不管什么 |
|------|------|--------|----------|
| ECS | 纯模拟层 | 数据存储、系统调度、确定性 | 并发、网络、IO、时间来源 |
| Actor | 调度层 | 并发安全、消息路由、生命周期 | 游戏数据长什么样 |
| Gateway / Cluster | 网络层 | 连接、协议、寻址 | 模拟逻辑 |

## 2. 技术选型（已定）

| 层 | 选择 | 说明 |
|----|------|------|
| Actor 内核 | 参考 hollywood 设计 | ringbuffer 邮箱 + 按需 goroutine + 批处理、类型化消息、崩溃缓冲 + 重启、PID 寻址、kind 激活 |
| 客户端协议 | pomelo 协议 | 客户端 ↔ 网关：握手/心跳/route/消息类型；兼容 Unity/Cocos/微信等现成 SDK；实现蓝本 = cherry `net/parser/pomelo` |
| 服务器间通信 | hollywood remote/cluster（dRPC） | 网关 ↔ 世界节点、节点 ↔ 节点；类型化 protobuf 消息，**不走 pomelo** |
| ECS | 自研 | 稀疏集 + 泛型查询 + 惰性注册表（见 §4） |
| 网关 | 自研（四件套之一） | pomelo parser + agent/session + 路由注册表（见 §6） |

```text
客户端（Unity / Cocos / Web / 微信）
   ⇅ pomelo 协议（握手、心跳、route、request/notify/response/push）
Gate 节点（pomelo parser + agent actor）
   ⇅ hollywood remote/cluster（dRPC，类型化 protobuf 消息）
World 节点（world actor = hollywood actor + ECS World）
```

### 2.1 为什么选 hollywood 而非 cherry（讨论记录）

两个项目本质不在一个层次：**hollywood 是通用 actor 内核，cherry 是游戏服务器框架**。对比：

| 维度 | hollywood | cherry |
|------|-----------|--------|
| 定位 | 通用高性能 actor 框架（Proto.Actor 风格） | 游戏服务器框架（pomelo 路线） |
| 并发模型 | ringbuffer 邮箱 + 按需 goroutine + 批处理（默认 300 条/轮）；actor 空闲零成本 | 每 actor 常驻 goroutine，select 三队列逐条处理 |
| 消息分发 | 类型化（`Receive(ctx)` + `switch type`），无反射 | 字符串注册 + 反射（为网络路由妥协） |
| 可靠性 | 崩溃缓冲（panic 不丢消息）+ 自动重启（maxRestarts）+ 子 actor 监督清理 | panic 只 recover 记日志，无重启无监督 |
| 远程/集群 | dRPC 传输 + 流式路由；发现用 zeroconf/Consul；kind 激活（分布式拉起 actor） | NATS 传输 + profile/nats/etcd 三发现后端；节点类型 + 随机路由 |
| 网关/游戏基建 | 无（要自建） | 内置：TCP/WS、pomelo/simple 协议、agent/会话/心跳/踢线、消息池、日志、错误码 |

结论（讨论定案）：

1. **actor 内核参考 hollywood**：ringbuffer + 按需 goroutine + 批处理、类型化消息、崩溃缓冲 + 重启，是通用 actor 内核的正确形态，比 cherry 的"常驻 goroutine + 反射路由"先进一个量级。
2. **游戏层设计参考 cherry**：agent 单写者写队列、会话状态机、pomelo parser 是网关层的实现蓝本。
3. **客户端协议取 pomelo**：兼容现成客户端 SDK 生态；**服务器间通信保留 hollywood dRPC**，pomelo 只在客户端 ↔ 网关这一层出现。

### 2.2 关键接缝：路由注册表（route → 类型化消息）

pomelo 的路由是字符串，hollywood 的分发是类型，两者靠一张注册表对接：

```text
客户端消息 route = "world.player.move"（携带二进制 payload）
   → 网关查路由注册表：route →（目标 actor kind/pid，消息类型 WorldPlayerMove）
   → 反序列化 payload 为类型化消息
   → 发送到 world actor 的 PID
```

- 路由与消息类型的对应关系是客户端/服务端的"协议契约"，定义在共享 protobuf 文件中。
- pomelo 只提供信封层（package/message/route/心跳），消息体用标准 protobuf 编码，与 hollywood 内部消息复用同一套 proto 定义。
- 需要对接现成 SDK 的 pomelo protobuf 压缩时，移植 cherry `net/parser/pomelo` 的 dict/protos 实现。
- 可移植部分：`packet` 层（握手/ack/心跳/数据/踢线封装）、`message` 层（flag、mid varint、route 字典、四种消息类型）、agent 模型（连接生命周期状态机、串行写队列、心跳超时、踢线）。

## 3. Actor 模块

### 3.1 职责

- 每 actor 独立执行单元，消息串行处理（无锁）
- 有界邮箱 + 定时器
- PID 寻址（address + id）
- 子 actor、生命周期、panic 隔离、崩溃缓冲 + 自动重启

### 3.2 设计决策

最终选型为 hollywood 风格（§2.1 已记录理由）。保留 cherry 模型作为对照与取舍记录：

| # | 决策点 | hollywood 方案（已选） | cherry 对照 |
|---|--------|------------------------|-------------|
| 1 | 邮箱 | ringbuffer 有界队列 + 按需 goroutine + 批处理 | 常驻 goroutine + select 三队列（local/remote/event） |
| 2 | 消息分发 | 类型化：`Receive(ctx)` + `switch msg := ctx.Message().(type)` | 字符串注册 + 反射 |
| 3 | 可靠性 | 崩溃缓冲（panic 不丢消息）+ 自动重启 + 子 actor 监督 | panic recover 记日志，无监督 |
| 4 | 同步调用 | `Request(pid, msg, timeout)` 返回 `*Response`，超时丢弃迟到回复 | `CallWait` + 固定超时 |
| 5 | 寻址 | PID（address + kind/id），cluster kind 激活 | Path（nodeID.actorID.childID） |

注意点：

- **tick 内禁止同步跨 actor 调用**：世界 actor 的 tick 循环里不做 `Request`，否则会阻塞在世界之外的消息上（死锁/卡顿温床）。
- 超时默认值需要可配置：cherry 的 `executionTimeout=100ms` 对 tick 循环（一个 tick 处理一批意图）过紧，世界 actor 建议 300ms。
- 消息池/零分配 MVP 不做：引用计数是 bug 温床，先保证语义正确，等 profiling 有数据再上。

### 3.3 接口契约（草案，按 hollywood 风格）

```go
// PID：进程标识（address + id），hollywood 风格
type PID struct {
    Address string // 节点地址，本地为 LocalLookupAddr
    ID      string // kind/id，如 "world/room-1"
}

// Receiver：所有 actor 实现该接口，消息按类型分发
type Receiver interface {
    Receive(ctx *Context)
}

// Context：当前消息上下文
type Context interface {
    PID() *PID
    Sender() *PID
    Message() any

    Send(pid *PID, msg any)
    Request(pid *PID, msg any, timeout time.Duration) *Response
    Respond(msg any)
    SpawnChild(producer Producer, name string) *PID
    SendRepeat(pid *PID, msg any, interval time.Duration) SendRepeater
}

// Engine：actor 运行时（spawn/send/request/broadcast）
type Engine interface {
    Spawn(producer Producer, kind string, opts ...OptFunc) *PID
    Send(pid *PID, msg any)
    Request(pid *PID, msg any, timeout time.Duration) *Response
    BroadcastEvent(msg any)
}
```

## 4. ECS 模块

### 4.1 核心概念

一句话：**ECS 把"数据"和"逻辑"彻底分开**。可以想象成一个极简的内存数据库：实体 ID 是主键，组件是列，系统是对某几列做的批量查询更新。

- Entity = 数字 ID，本身无数据
- Component = 挂在 ID 上的纯数据（Position / Health / Hunger / ...）
- System = 批量逻辑，扫描"拥有某几种组件"的实体
- Resource = 全局单例（世界时钟、RNG、配置）

示例（饥荒类世界）：

| ID | 是什么 | Position | Health | Hunger | Brain | Growable |
|----|--------|----------|--------|--------|-------|----------|
| 1 | 玩家 | (3,5) | 100 | 80 | – | – |
| 2 | 树 | (8,2) | 50 | – | – | 阶段2 |
| 3 | 猪人 | (10,4) | 60 | 70 | 巡逻 | – |
| 4 | 篝火 | (6,6) | – | – | – | – |

系统按组件扫描：饥饿系统扫 Hunger（树和篝火被跳过）；伤害系统扫 Position + Health（篝火被跳过）；生长系统扫 Growable（只有树）。**行为由组件组合决定，无继承**。

为什么组件组合优于继承：树和猪人都要能被砍/被打（都有 Health），树和玩家都要能着火，猪人和玩家都要饿——这些横切行为用继承要发明一堆父类和接口（Damageable/Burnable/Hungry），每次加横切行为都要动类层次；用 ECS 就是"加一个组件 + 加一个系统"，不碰已有代码。饥荒本体（Klei 的 Lua 组件系统）走的正是这个思路。

### 4.2 设计决策（已定）

| # | 决策点 | 选择 | 理由 |
|---|--------|------|------|
| 1 | 存储 | 每组件类型一张稀疏集；实体 ID 密集分配 + 空闲列表复用；World 维护每实体组件掩码（uint64 平铺，bit=存储创建顺序，组件类型超 64 时多字自动扩容） | 性能/缓存友好；迭代顺序 = 实体 ID 序（确定性）；掩码让销毁只遍历实体实际拥有的组件（O(实际组件数)），并支持"实体→组件"反查 |
| 2 | 查询与注册 | 泛型 API（Query / Query2）+ reflect.Type 惰性注册表 | 类型安全在 API 层，异质存储藏在内部 |
| 3 | 系统与确定性 | 固定顺序 slice；固定 dt；不用 map 迭代；RNG / 时钟收进 Resource（L1+L2） | 回放、离线测试、服务器间一致性验证 |
| 4 | 事件与 dirty | 组件增删/实体销毁走事件队列（tick 内消费）；dirty 集合供快照消费 | dirty 是 ECS → 网络层的接口 |
| 5 | 序列化 | 组件注册表存名称 + 编解码器；存档/快照遍历组件表 | 存档、快照、配表共用组件元数据 |
| 6 | 组件生命周期（已实现） | `ILifecycleAdd`（OnAdd）/ `ILifecycleRemove`（OnRemove）接口由 ECS 内核统一触发：Add 挂载后、Remove 移除前、DestroyEntity 两段式（先触发所有 OnRemove 再批量清理）；副作用与组件定义同处登记（如 Block 的 OnAdd/OnRemove 读写地图阻挡层）。Destroy 按实体掩码位号升序触发钩子（= 存储创建顺序），保证确定性 | 不再需要"调用方记得走包裹层"的纪律；组件包不能引用具体资源时用抽象接口（components.BlockTarget）+ `ecs.TryResourceOf` 解耦，避免循环依赖 |

### 4.3 原理详解：稀疏集、泛型查找、惰性注册表（讨论记录）

#### 4.3.1 稀疏集（Sparse Set）：组件数据怎么存

要解决的问题：几千个实体里，每种组件都要存一份数据，要求增删查都快、遍历顺序确定、内存紧凑。`map[实体ID]组件` 查得快，但 Go map 迭代顺序随机（确定性没了），且值散落内存各处（缓存不友好）。

稀疏集用两个数组：

```text
假设实体 1=玩家、2=树、3=猪人，只有它们有 Health 组件

Health 的存储：
sparse:   [ _, 0, 1, 2, _ ]     ← 下标=实体ID，值=dense 中的下标（_ = 没有）
dense:    [ Health(100), Health(50), Health(60) ]
entities: [ 1, 2, 3 ]           ← dense 每个位置对应的实体 ID
```

- `dense` 是紧凑连续数组，真正的数据都在这，遍历等于顺序扫一排内存，缓存友好。
- `sparse` 是稀疏映射表：按实体 ID 索引，每格只存一个 int，大部分是空的——"稀疏"因此得名。

三个操作都是 O(1)：

```text
Add(实体2, Health{50}):
    sparse[2] = len(dense)          // = 1
    dense = [Health(100), Health(50)]
    entities = [1, 2]

Get(实体3):  dense[sparse[3]]  →  dense[2] = Health(60)

Remove(实体2) —— 交换删除：
    把 dense 最后一个元素挪到被删的位置，再收缩一格
    dense = [Health(100), Health(60)]，entities = [1, 3]
```

细节：交换删除会改变遍历顺序（不再是严格的实体 ID 升序）。对确定性来说**不是问题**——回放要求"同样的操作序列产生同样的结果"，交换删除在此意义下是确定的。若未来需要严格升序，可加墓碑延迟压缩，MVP 不需要。这是游戏引擎真实在用的方案（C++ 的 EnTT 是稀疏集路线的代表）。

#### 4.3.2 泛型查找：系统怎么"查到"组件

每种组件类型 `T` 都有自己的稀疏集。`Query[T]` 顺着 `T` 的 dense 数组走一遍；`Query2[A, B]` **遍历两个集合中较小的那个，另一个用 sparse 表 O(1) 反查**：

```go
// Query2 的原理（伪代码）
func (w *World) Query2[A, B any](fn func(e Entity, a *A, b *B)) {
    sa := w.storage[A]()          // A 的稀疏集
    sb := w.storage[B]()          // B 的稀疏集

    // 遍历较小的集合，另一个用 sparse 表反查（O(1)）
    for i := 0; i < sa.Len(); i++ {
        e := sa.Entities()[i]
        if sb.Has(e) {
            fn(e, &sa.Dense()[i], sb.Get(e))
        }
    }
}
```

泛型的价值在类型安全：回调参数是 `*Hunger`，写错类型编译期就报错。Go 泛型是编译期为每种具体类型生成代码的，**回调热循环里没有反射**——可以放心在 tick 里调用。

#### 4.3.3 惰性注册表：怎么找到"组件 T 的存储"

World 在创建时并不知道会有哪些组件类型——`Health`、`Thirst`、`Brain` 都是 AI 后面一个个加进来的，不可能要求"用之前先注册"。惰性注册表就是 World 内部一张 `map[reflect.Type]存储`：

```go
type World struct {
    storages map[reflect.Type]any // 组件类型 → 它的稀疏集
}

func (w *World) storage[T any]() *sparseSet[T] {
    t := reflect.TypeOf((*T)(nil)).Elem()
    s, ok := w.storages[t]
    if !ok {
        s = newSparseSet[T]()      // 第一次用到才创建
        w.storages[t] = s
    }
    return s.(*sparseSet[T])
}
```

"惰性" = 第一次 `Add[T]` 或 `Query[T]` 时才创建 T 的稀疏集。好处：AI 加玩法组件 = 写个 struct 直接用，零注册仪式；反射只用来"类型当 key 查表"这一次，数据操作全是泛型不进热循环；序列化编解码器也挂在这张表上（存档/快照遍历组件表即遍历注册表）。

三者串起来：

```text
Query[Hunger] 泛型查找
      │
      ▼
惰性注册表：map[reflect.Type]存储 → 没有就创建
      │
      ▼
稀疏集：dense 紧凑数组（数据）+ sparse 映射（实体ID→下标）
      │
      ▼
遍历 dense → 确定性顺序 + 缓存友好 + O(1) 增删查
```

取舍一句话：**类型安全留给泛型，异质存储藏进反射注册表，性能交给紧凑数组**——AI 写系统只需要认识 `Add/Get/Query` 三个词。

### 4.4 确定性纪律（L2，已定，AI 写玩法必须遵守）

1. 随机数一律从 `world.Resource[RNG]` 取，禁止全局 `rand` 与系统内 `time.Now()`。
2. 实体 ID 分配顺序确定（密集分配 + 空闲列表复用），不做随机 ID。
3. 系统按固定顺序执行，迭代一律走稀疏集，禁止用 map 迭代。
4. 世界时钟由 tick 累计（`worldTime = tickCount * dt`），存档恢复时从存档读取。

目的：录像回放、服务器版本间一致性验证、离线模拟测试。**不做 L3（lockstep 级完全确定性）**，客户端不参与模拟，无此需求。

### 4.5 接口契约（草案）

```go
type Entity uint64

type World struct{ ... }

func (w *World) CreateEntity() Entity
func (w *World) DestroyEntity(e Entity)
func (w *World) Add[T any](e Entity, c T)
func (w *World) Get[T any](e Entity) *T
func (w *World) Has[T any](e Entity) bool
func (w *World) Remove[T any](e Entity)

// 查询：泛型 + 稀疏集遍历，顺序 = 实体 ID 序
func (w *World) Query[T any](fn func(e Entity, c *T))
func (w *World) Query2[A, B any](fn func(e Entity, a *A, b *B))

// 系统：固定顺序注册
type System interface {
    Update(w *World, dt time.Duration)
}
func (w *World) AddSystem(order int, s System)

// Resource：全局单例（世界时钟、RNG、配置）
func (w *World) AddResource(r any)
func (w *World) Resource[T any]() *T

// dirty：组件变更标记，供快照系统消费
func (w *World) MarkDirty(e Entity, comps ...any)
func (w *World) DrainDirty() map[Entity][]ComponentID
```

## 5. Actor ↔ ECS 接缝

### 5.1 为什么实体不是 actor（讨论记录）

cherry 的 actor 粒度适合"玩家、房间、服务"这类低频有界状态，不适合 DST 这种持续模拟的世界：

- 世界实体数量级是数千，actor 的 goroutine + mailbox 开销会被放大；
- 实体间交互（攻击、掉落、燃烧传播、群体仇恨）若跨 actor 就是消息风暴；
- 单一 sim goroutine 内所有实体共享状态，写起来像单机游戏，存档天然一致，调试也简单。

**跨世界玩法**（玩家从世界 A 串门到世界 B）通过世界 actor 之间的 RPC 实现，而不是实体 actor 跨节点。垂直的"世界边界"比水平的"实体边界"更符合游戏形态。

### 5.2 命令缓冲 + outbox（tick 流程与纪律）

```text
actor inbox（玩家意图/跨 actor 消息）
        ↓
命令缓冲（两 tick 之间积攒）
        ↓
tick：ECS systems 按固定顺序执行（纯函数）
        ↓
outbox（效果队列：推送快照 / 跨 actor 消息 / 存档）
        ↓
tick 结束后 actor 统一 drain
```

纪律：

1. ECS 系统是纯函数：不调 actor API、不发网络消息。
2. 副作用通过 outbox 表达（`Emit(Push{...})` / `Emit(SendMessage{...})`），由 actor drain。
3. 时间由 actor 注入固定 dt（10Hz → 100ms），ECS 不读 wall clock。
4. 玩家意图先入命令缓冲、tick 统一消费——消息到达速率与模拟速率解耦。

```go
type WorldActor struct {
    sim      *ecs.World
    commands []Command // 玩家意图缓冲
    outbox   []Effect  // tick 副作用
}

func (a *WorldActor) onAction(cmd Command) {
    a.commands = append(a.commands, cmd) // 只入缓冲，不立即执行
}

func (a *WorldActor) onTick() {
    a.sim.ApplyCommands(a.commands) // 1. 命令 → ECS（校验 + 执行）
    a.commands = a.commands[:0]
    a.sim.RunSystems(dt)            // 2. 系统（纯函数）
    a.flushOutbox()                 // 3. drain 效果（推送 / 消息 / 存档）
}
```

## 6. Gateway 模块（已定，MVP 按推荐）

### 6.1 边界

Gateway 做四件事：**接连接、解协议、管会话、转路由**。不做模拟（那是 ECS）、不做玩家数据（那是世界 actor）。

```text
客户端 ⇅ pomelo 字节流（握手/心跳/数据包）
   │
连接器（TCP / WS 监听、accept、TLS）
   │
agent actor（每连接一个：状态机、写队列、心跳、踢线）
   │
协议层（pomelo package/message 编解码）
   │
路由注册表（route → 消息类型 → 目标 PID）
   │
hollywood engine / cluster（发往世界 actor / 接收推送）
```

### 6.2 核心设计：agent 模型（从 cherry 学的最有价值的一条）

每个连接 = 一个 hollywood actor（kind="agent"，ID 即 SID）。内部处理类型化消息：

```go
type ConnectionOpened struct{ Conn net.Conn }   // 连接建立
type PacketReceived struct{ Pkt *pomelo.Packet } // 读到数据
type WriteRequest struct{ Data []byte }          // 要写出的数据
type HeartbeatTimeout struct{}                   // 心跳超时
type Kick struct{ Reason string }                // 踢线
```

三条铁律：

1. **单写者**：所有写出操作进同一个写队列，由专门的写 goroutine 串行写 socket。多个 actor 同时推送给一个玩家时，没有单写者就是并发写 socket 的数据竞争。
2. **读循环与 actor 解耦**：读 goroutine 把收到的包转成 `PacketReceived` 发给 agent actor，agent 串行处理，actor 永不阻塞在 socket 读上。
3. **生命周期状态机**：Init（握手前）→ WaitAck（等握手 ack）→ Working（正常）→ Closed。心跳超时（2×间隔）与踢线（0x05 kick 包）都走状态机，关闭路径唯一。

### 6.3 路由注册表（关键接缝，见 §2.2）

```go
type RouteEntry struct {
    MsgType any            // 反序列化目标，如 *WorldPlayerMove
    Target  TargetRule     // 固定 PID / kind / 按 UID 查
}

type Router interface {
    Register(route string, entry RouteEntry)
    Resolve(route string) (RouteEntry, bool)
}
```

消息流：

```text
Request：客户端 mid=1, route="world.player.move", body=字节
  → 查路由表，得到消息类型 *WorldPlayerMove
  → 反序列化 body → 发给世界 actor PID
  → 世界 actor 处理完 Respond(*WorldPlayerMoveResp)
  → 网关按 mid=1 组 pomelo response 包，入写队列

Notify：mid=0，只发不收，无响应路径

Push：世界 actor → agent PID（推送快照/事件）
  → agent 组 pomelo push 包（带 route），入写队列
```

三个关联关系：

- **mid 关联**：request 进来时记下 mid；响应消息自带请求上下文（uid + mid），由 agent 直接组包——不维护 pending 表，天然支持超时丢弃。
- **推送寻址**：玩家进世界时，gate 把 agent PID 带给世界 actor，并绑定 UID↔PID。
- **反序列化时机**：在网关做（按 route 查类型），世界 actor 收到的是已解码的类型化消息；不把反序列化下沉到玩法 actor。

### 6.4 会话与在线

- SID = agent actor ID（连接级）；UID = 玩家账号（登录后绑定）。
- 绑定表 `UID → agent PID` 放网关节点（登录绑定、断线解绑），供"按 UID 踢线/推送"用。
- 断线：agent Closed → 通知世界 actor 走离线保护（实体保留 N 分钟）。
- 同 UID 重复登录：踢掉旧连接（pomelo kick 包）。

### 6.5 已定决策（MVP）

| # | 决策 | 结论 |
|---|------|------|
| Q4 | 连接器 | **WS 优先**（浏览器/调试直连），TCP 二期；协议编解码同一套 |
| Q5 | 登录时序 | **握手保持纯净**（只做版本/心跳协商），连接后 `login` 请求校验 token |
| Q6 | 断线重连 | **最小重连**：不做连接迁移；断线实体保留（离线保护），重新登录直接接回原实体 |
| – | route 压缩 | 二期再做；协议层留 `RouteEncoder` 接口 |
| – | 单机 MVP | 网关与世界同 engine（本地 PID 直连），跨节点是 Stage 3 |

### 6.6 接口契约（草案）

```go
// 路由注册表
type RouteEntry struct {
    MsgType any        // 反序列化目标，如 *WorldPlayerMove
    Target  TargetRule // 固定 PID / kind / 按 UID 查
}
type Router interface {
    Register(route string, entry RouteEntry)
    Resolve(route string) (RouteEntry, bool)
}

// Agent（每连接一个）
type Agent interface {
    SID() string
    UID() int64
    State() AgentState // Init / WaitAck / Working / Closed
    Push(route string, msg any)
    Kick(reason string)
    OnClose(fn func())
}

// 会话绑定
type SessionTable interface {
    Bind(uid int64, pid *PID)
    Unbind(uid int64)
    Lookup(uid int64) (*PID, bool)
}
```

## 7. 已定决策汇总

| # | 决策 | 结论 |
|---|------|------|
| Q1 | ECS 存储/查询形态 | **稀疏集 + 泛型查询 + 惰性注册表**（原理见 §4.3） |
| Q2 | Actor 信箱 | **hollywood ringbuffer 有界队列 + 按需 goroutine + 批处理** |
| Q3 | 确定性程度 | **L1 固定系统顺序 + L2 种子化随机**（不做 L3 lockstep） |
| Q4 | Gateway 连接器 | **WS 优先**，TCP 二期 |
| Q5 | Gateway 登录时序 | **握手纯净 + 连接后 `login` 请求** |
| Q6 | Gateway 断线重连 | **最小重连**（离线保护 + 重登接回），不做连接迁移 |

Actor + ECS + Gateway 契约已锁定，剩余四件套之一为 Cluster（单机开发用不上，Stage 3 再设计）。

## 8. 构建顺序

Actor + ECS（执行内核）→ Gateway（pomelo 协议，能联机看到东西）→ Cluster（最后，单机开发用不上）。

每完成一个模块即可让 AI 基于接口开始做对应层：ECS 一完成，饥荒生存组件（饥饿 / 昼夜 / 生长）即可开写。

## 9. 下一步

- 已定：Actor + ECS + Gateway 的契约全部锁定。
- 待续：Cluster 设计（Stage 3 前不需要）或直接开新项目搭骨架（Actor + ECS → Gateway 最小闭环：WS 连接 + world actor 10Hz tick + 玩家移动 + 广播位置）。
