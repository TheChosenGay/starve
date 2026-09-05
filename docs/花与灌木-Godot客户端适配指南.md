# 花与灌木：Godot 客户端适配指南

> 服务端仓库：`/Users/daishan/starve`  
> 客户端仓库：`/Users/daishan/starve-godot`  
> 目标客户端：Godot 4.7.1 .NET / C#

## 1. 服务端契约

本次新增以下稳定协议值：

```proto
ITEM_KIND_FLOWER = 7; // 花：世界环境物
ITEM_KIND_PETAL = 8;  // 花瓣：背包物品
ITEM_KIND_SHRUB = 9;  // 灌木：纯环境物

message Scenery {
  ItemKind kind = 1;
}
```

`TemplateConfig` 新增：

```proto
ItemKind pick_yield = 9;
```

`pick_yield = ITEM_KIND_UNSPECIFIED` 表示采集产物与目标 `Pickable.kind`
相同，以兼容现有浆果；花的模板配置为 `FLOWER → PETAL`。

服务端实体组合如下：

- 花：`Position + Pickable{kind=FLOWER, work_left=1, max_work=1} + DropSource`
- 灌木：`Position + Scenery{kind=SHRUB}`
- 花瓣：仅作为 `Inventory.items[].kind=PETAL` 出现在背包中
- 花和灌木均不含 `Block`，不会阻挡移动或寻路
- 灌木不含 `Pickable`、`Choppable`、`Minable`、`DropSource`

当前花没有配置重生。采摘成功后：

1. 玩家进入 `ActionKind.Pick`；
2. 背包增加一个 `PETAL`；
3. 花的 `Pickable.work_left` 从 `1` 变为 `0`；
4. 花实体不会被删除，`Pickable` 组件也不会被移除。

因此客户端必须按 `work_left` 控制花的可见与可交互状态，不能等待
`removed_entities`。

## 2. 第一步：同步协议

在客户端仓库执行：

```bash
cd /Users/daishan/starve-godot
cp ../starve/pkg/proto/game/game.proto proto/game.proto
dotnet build Starve.Protocol/Starve.Protocol.csproj
```

`Starve.Protocol.csproj` 已通过 `Grpc.Tools` 在构建时生成 C# 类型，不需要手工维护
或提交 `Game.cs`。

同步后应能使用：

- `ItemKind.Flower`
- `ItemKind.Petal`
- `ItemKind.Shrub`
- `Scenery.Parser`
- `TemplateConfig.PickYield`

没有新增路由，`proto/message.proto`、`Starve.Protocol/Routes.cs` 和命令格式均不变。

协议校验：

```bash
python3 scripts/check_proto_sync.py --server-dir ../starve
```

## 3. 协议层适配

文件：`Starve.Protocol/World/WorldService.cs`

无需增加专用分支。`WorldService` 已按组件名保存原始字节，`Scenery` 会和其他组件
一样进入 `EntityView.Components`，使用时懒解析：

```csharp
var scenery = view.Get("Scenery", Scenery.Parser);
```

不要把 `Scenery` 转成 `Block`，也不要加入客户端 `_blocked` 集合。

建议在 `ProtocolSmoke/Program.cs` 的全量快照检查中增加：

- 至少能解析一个 `Scenery{kind=SHRUB}`；
- 灌木有 `Position`，没有 `Block` 和三种工作目标组件；
- 花能解析为 `Pickable{kind=FLOWER}`，且没有 `Block`。

## 4. 世界实体识别与样式

文件：`GodotClient/Game/EntityVisual.cs`

当前 `StyleFor` 只识别工作目标、生物、建筑等组件。需要增加两类判断：

1. `Pickable.kind == ItemKind.Flower`：花；
2. `Scenery.kind == ItemKind.Shrub`：灌木。

建议不要只判断数值 `7/9`，统一使用生成后的枚举。

花是否可见应由以下条件决定：

```csharp
var flower = view.Get("Pickable", WorkTarget.Parser);
var flowerVisible = flower is { Kind: ItemKind.Flower, WorkLeft: > 0 };
```

灌木只依据 `Scenery.kind` 展示，永远不产生交互按钮。

若继续扩展 `EntityStyle`，可增加 `IsFlower`、`IsShrub`；不要把灌木标成
`IsTree`，否则会继承树的尺寸、受击闪白及砍伐视觉语义。

## 5. 3D 模型接入

涉及文件：

- `GodotClient/Game/ActorCatalog3D.cs`
- `GodotClient/Game/EntityLayer3D.cs`
- 建议新增 `GodotClient/Game/FlowerActor3D.cs`
- 建议新增 `GodotClient/Game/ShrubActor3D.cs`

在 `ActorCatalog3D.TryCreate` 中，优先于通用占位模型识别：

```csharp
var pickable = view.Get("Pickable", WorkTarget.Parser);
if (pickable?.Kind == ItemKind.Flower)
    return new FlowerActor3D();

var scenery = view.Get("Scenery", Scenery.Parser);
if (scenery?.Kind == ItemKind.Shrub)
    return new ShrubActor3D();
```

客户端目前的 `TreeActor3D` 实际加载
`res://assets/models/ghibli-bush/ghibli_bush_godot.glb`。灌木可以复用该 GLB，
但应通过独立的 `ShrubActor3D` 加载，不要直接返回 `TreeActor3D`，以免把纯装饰灌木
误当成可砍伐树。

`EntityLayer3D.SyncEntities` 当前会在每次同步末尾执行 `node.Visible = true`。
需要在这里增加花的耗尽可见性处理：

```csharp
node.Visible = !IsDepletedFlower(view);
```

或者让 `FlowerActor3D` 暴露 `ApplyState(WorkTarget)`，统一处理有花/采空状态。
该判断必须同时覆盖全量快照和增量快照。

如果花模型尚未导入，可先使用 `ActorMesh3D` 的小型占位物保证协议闭环；最终资源建议
放入：

```text
GodotClient/assets/models/flower/
```

模型脚底原点应与现有实体一致，节点自身不要附加碰撞体。

## 6. 2D 回退渲染

涉及文件：

- `GodotClient/Game/EntityLayer.cs`
- `GodotClient/Game/EntityVisual.cs`

`--render-2d` 仍是受支持的回退模式，至少应做到：

- 花使用独立颜色或贴图；
- 灌木使用独立颜色或贴图；
- `FLOWER.work_left == 0` 时隐藏花；
- 灌木不显示工作量或动作提示。

即使当前主要验收 3D，也不要让 2D 模式把灌木显示成默认白色菱形。

## 7. 名称、交互和采摘产物

文件：`GodotClient/Game/GameRoot.cs`

### 7.1 名称

当前 `EntityName` 对所有 `Pickable` 硬编码为“浆果丛·采集→浆果”，需要改为读取
模板：

1. 实体名取 `Pickable.kind` 对应的 `TemplateConfig.name`；
2. 产物取该模板的 `pick_yield`；
3. `pick_yield == 0` 时回退到 `Pickable.kind`。

预期：

- 浆果：`浆果·采集→浆果`
- 花：`花·采集→花瓣`
- 灌木：`灌木`，不带动作提示

`DescribeSelected` 同样应使用模板名；解析到 `Scenery{SHRUB}` 时显示“灌木”，不要
显示成“实体 #id”。

### 7.2 点击交互

现有点击分派已经按 `Pickable` 发送 `Gather`，花无需新增命令：

```text
Pickable → Intent.Gather → CommandService.Gather(entityId)
```

需要把错误文案“目标不可采集（不是浆果丛）”改为“目标不可采集”。

客户端当前统一用曼哈顿距离 `<= 2` 做工作动作预检，但服务端裸手
`Picker.Range == 1`。花的本地采摘提示应按距离 `<= 1`，或进一步按动作能力分别
维护范围；服务端仍是最终权威判定。

灌木没有工作目标组件，点击时不得发送 Gather/Chop/Mine。为避免大量装饰物抢占
点击，可在正常玩法的 `FindNearest` 中跳过仅有 `Scenery` 的实体；调试面板仍可通过
`World3DView.TryPickVisual` 点选模型。

### 7.3 自动行为

空格 `AutomateMode.Any` 不需客户端改动。目标选择由服务端完成：

- 未采摘的花会作为 `Pickable` 候选；
- `work_left == 0` 的花会被跳过；
- 灌木没有行为组件，不会成为候选。

## 8. 背包与花瓣图标

涉及文件：

- `GodotClient/Game/GameRoot.cs`
- `GodotClient/Game/InventorySlot.cs`

背包已有模板驱动的名称、颜色和堆叠处理。协议同步后，`PETAL` 会自动显示为
“花瓣”，模板颜色为粉色。

若已有花瓣图标，在 `GameRoot.ItemIconFiles` 增加：

```csharp
[(int)ItemKind.Petal] = "res://assets/items/petal.png",
```

没有图标时会自动回退为模板色块，不影响功能验收。`FLOWER` 和 `SHRUB` 不应进入
背包图标表。

客户端可以读取 `TemplateConfig.PickYield` 做提示，但不能自行增加花瓣；背包增量
必须以服务端下发的 `Inventory` 为唯一事实源。

## 9. 小地图与阻挡

涉及文件：

- `GodotClient/Game/MinimapView.cs`
- `GodotClient/Game/GameRoot.cs` 的阻挡缓存

花和灌木数量较多。建议小地图默认跳过 `Scenery` 和花，避免装饰点淹没玩家、建筑和
生物标记；如需显示，使用更小、透明度更低的点。

阻挡缓存已经只读取 `Block + Position`，不需要修改。验收时应确认角色能直接穿过花
和灌木所在格。

## 10. 推荐测试

建议在客户端增加以下覆盖：

1. 协议测试：`Scenery`、三个新枚举和 `TemplateConfig.PickYield` 可序列化往返；
2. 世界合并测试：全量/增量快照中的 `Scenery` 能保留在 `EntityView`；
3. 分类测试：花识别为 Flower、灌木识别为 Shrub，不误判为 Tree；
4. 状态测试：花 `work_left: 1 → 0` 后隐藏，实体仍存在时不会再次发送采集；
5. UI 测试：花描述为“采集→花瓣”，背包 `PETAL` 显示“花瓣”；
6. 碰撞测试：花和灌木均不进入 `_blocked`；
7. 冒烟测试：客户端能解析生产地图中的花和灌木。

完整验收命令：

```bash
cd /Users/daishan/starve-godot
make check

# 启动服务端后再执行在线冒烟
STARVE_GATE_URL=ws://127.0.0.1:8081/ws make e2e
```

手工验收：

- 出生点附近能看到花和灌木；
- 可穿过两者所在格；
- 点击或空格可以采花；
- 采花后花隐藏，背包增加一个花瓣；
- 灌木不可采集、不可砍伐且不会抢占正常交互；
- 重连后已采空的花仍保持隐藏。

## 11. 最小改动清单

必须修改：

- `proto/game.proto`
- `GodotClient/Game/EntityVisual.cs`
- `GodotClient/Game/ActorCatalog3D.cs`
- `GodotClient/Game/EntityLayer3D.cs`
- `GodotClient/Game/GameRoot.cs`

建议新增：

- `GodotClient/Game/FlowerActor3D.cs`
- `GodotClient/Game/ShrubActor3D.cs`
- 花瓣背包图标
- 对应协议、分类和状态测试

通常无需修改：

- `Starve.Protocol/World/WorldService.cs`
- `Starve.Protocol/CommandService.cs`
- `Starve.Protocol/Routes.cs`
- 移动预测和 Block 处理
