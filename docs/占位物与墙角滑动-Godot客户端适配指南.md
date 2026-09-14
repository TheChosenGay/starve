# 占位物与墙角滑动：Godot 客户端适配指南

> 服务端仓库：`/Users/daishan/starve`  
> 客户端仓库：`/Users/daishan/starve-godot`  
> 目标客户端：Godot 4.7.1 .NET / C#  
> 对照文档：本仓库 [`P1.1-移动预测协议契约`](P1.1-移动预测协议契约.md)、客户端 `P1.4-PRESENTATION-LAYERS.md`

## 1. 服务端改了什么（占位 ≠ 不可走）

以前"阻挡"只有一种：整格 `Block`，格子不可走、角色停在相邻格的边界上——树只占格子中间
一小块，看着就是"离树老远就停了"。现在拆成三层，各答一个问题：

| 层 | 数据 | 答什么问题 | 谁写 |
|---|---|---|---|
| 地形层 | `MapData.Walkable`（水/悬崖） | 能不能走进这格 | 地图生成 |
| 占位层 | `Block` → `MapData.Occupied` 代价 | 能不能在这格放东西 / 值不值得绕 | 占位物实体 |
| 形状层 | 格心圆柱（圆）/ 占格盒（矩形） | 能贴到多近、撞上怎么滑 | 占位物实体 |

关键结论：

- **占位格仍然可走**。占位只做两件事：放置冲突（一格只归一个占位物）+ 寻路代价
  （穿过占位格要加 `OccupiedCostThin=6`（圆）/ `OccupiedCostFull=1000`（盒），A\* 因此绕开，
  但完全被围死时仍然给得出路径——占位不是硬墙）。
- **物理阻挡由形状决定**：`Block.radius > 0` 是格心圆（树/岩：占 1 格，走不满一格），
  `Block.radius == 0` 是 `Block.width × Block.height` 的占格盒（建筑/工作站/城墙）。
  移动是"连续扫掠 + 沿接触切面滑动"，所以能贴着树干/墙面走、撞上顺着滑开。
- **硬墙只剩地形**（水/悬崖）：`Walkable` 只看地形，跨格不可走就贴边停在边界外侧。

移动体缺省碰撞半径是服务端常量 `systems.BodyRadius = 0.305`（格，来自客户端玩家模型
pigman 的躯干推导）。但客户端**不要写死这个常量**：玩家半径由服务端在
`Moveable.body_radius` 里下发，客户端用 `OwnMovementSim.SetBodyRadius` 接收，
只在服务端没给值（`body_radius = 0`）时才回退到缺省
（客户端侧是 `OwnMovementSim.DefaultBodyRadius = 0.305f`）。四足生物另有
`body_half_length`：段沿朝向铺开的胶囊。

## 1.1 调试：把碰撞体画出来

服务端开 `GATE_DEBUG_COLLISION=1` 后，每个有碰撞体的实体会多一个 `DebugShape` 组件随快照下发：

```proto
message DebugShape {
  enum Kind { DEBUG_SHAPE_KIND_UNSPECIFIED = 0; DEBUG_SHAPE_KIND_CAPSULE = 1; DEBUG_SHAPE_KIND_BOX = 2; }
  Kind kind = 1; double radius = 2;
  double a_x = 3; double a_y = 4; double a_z = 5;   // 胶囊段起点（模型局部空间，格）
  double b_x = 6; double b_y = 7; double b_z = 8;   // 段终点
  double width = 9; double depth = 10; double height = 11; // 盒
  string source = 12;
}
```

客户端 `DebugShapeLayer3D` 把它画成半透明体（跟随实体节点的位置与朝向），
用来肉眼核对"碰撞体是不是刚好包住渲染模型"：树/岩是格心圆柱、建筑是占格盒、
玩家/生物是身体胶囊（四足的段沿朝向铺开）。关掉开关组件会被摘掉，客户端随之隐藏。

## 2. 客户端要改的四处

### 2.1 可行走网格：只留地形

`GameRoot.RebuildBlocked`（按 `Block` 组件建的 `_blocked`）现在语义错了：占位格不该判成不可走。
改成**只按地形判**（水面不可走），`_blocked` 这套动态阻挡直接删掉：

```csharp
/// <summary>与服务端 Walkable 一致：只看地形（水/悬崖），占位物不算墙。</summary>
private bool IsWalkable(int x, int y)
{
    if (_tilemap is null) return true;
    if (x < 0 || y < 0 || x >= _tilemap.Width || y >= _tilemap.Height) return false;
    return !_tilemap.IsWater(x, y);   // 具体取水面判定，见 TileMap
}
```

### 2.2 形状层：收集圆与盒

占位物随快照下发（`Block` 组件：`width/height/radius`），收集成形状列表交给 `OwnMovementSim`：

```csharp
// GameRoot：圆 = 格心（Position + 0.5）+ 半径；盒 = 左上角 Position + width×height
private readonly List<BlockerShape> _blockers = new();

private void RebuildBlockers(IReadOnlyDictionary<ulong, EntityView> entities)
{
    _blockers.Clear();
    foreach (var view in entities.Values)
    {
        var b = view.Get("Block", Block.Parser);
        var p = view.Get("Position", Position.Parser);
        if (b is null || p is null) continue;
        if (b.Radius > 0)
        {
            _blockers.Add(BlockerShape.Circle(p.X + 0.5f, p.Y + 0.5f, (float)b.Radius));
            continue;
        }
        _blockers.Add(BlockerShape.Box(p.X, p.Y, Math.Max(1, b.Width), Math.Max(1, b.Height)));
    }
    _ownSim?.SetBlockers(_blockers); // OwnMovementSim 每 tick 按需取用
}
```

视觉钉点也要跟着走：`EntityLayer3D.RememberBlockFootprint`（以及 2D 层同类逻辑）现在只认
`Block`，而圆的视觉中心在格心——圆形状按 1×1 脚印处理即可（盒本来就带 width/height）：

```csharp
var block = view.Get("Block", Block.Parser);
if (block is not null)
    _blockFootprint[id] = (Math.Max(1, block.Width), Math.Max(1, block.Height));
```

### 2.3 寻路代价（客户端暂时不用做）

路径由服务端算（`worldmap.FindPath`），客户端不跑 A\*，所以占位代价不需要在客户端复刻。
等客户端出现本地寻路（例如预测性自动行走）时再照抄 `OccupiedCostAt` 的代价规则。

### 2.4 `OwnMovementSim`：扫掠 + 侧滑（含墙角兜底）

服务端一个 tick 的顺序是**形状层 → 格子层**（`systems.MoveBody`），客户端 50ms 分片必须一致：

```csharp
var dist = _speed * SlopeSpeed.Factor(pos.X, pos.Y, _dirX, _dirY, HeightAt) * sliceMs / 1000f;
if (_dirX != 0 && _dirY != 0) dist /= MathF.Sqrt(2f);

var stepX = _dirX * dist;
var stepY = _dirY * dist;
if (BodyRadius > 0 && _blockers.Count > 0)
{
    var (endX, endY) = Slide(pos.X, pos.Y, stepX, stepY, BodyRadius, _blockers);
    stepX = endX - pos.X;   // 滑动后的实际位移：方向可能已经变了
    stepY = endY - pos.Y;
}

if (stepX != 0)
    (_anchorX, _subX) = StepAxis(_anchorX, _subX, MathF.Sign(stepX), MathF.Abs(stepX),
        x => _walkable(x, _anchorY));
if (stepY != 0)
    (_anchorY, _subY) = StepAxis(_anchorY, _subY, MathF.Sign(stepY), MathF.Abs(stepY),
        y => _walkable(_anchorX, y));
```

`Slide` 的完整参考实现落在客户端 **`Starve.Core/MovementSlide.cs`**（常量：`Skin=1e-3`、
`MaxSlides=4`、`CornerProbeStep=0.1`；半径由 `OwnMovementSim` 传入，缺省
`DefaultBodyRadius=0.305f`），与服务端 `pkg/collide.SweepSlideSphere`
同几何。循环要点（顺序不能改）：

1. 求最早接触 `t`（圆 × 圆闭式解；圆 × 盒用膨胀盒 slab + 角区改解角圆）；
2. 推进到接触点，沿法向（指向移动体）退出 `Skin`；
3. 剩余位移投影到接触切面（去掉法向分量）；还有分量就回到第 1 步（最多 4 次）；
4. 投影后一点都不剩（8 向输入正对角撞墙角）：按 `CornerProbeStep` 离散推进 + 沿较近轴
   推出，顺着墙面滑开；正对平面推时结果仍是停住。

金标准向量（`movement_golden.json` 的 9 条 `shape_*`）是唯一判据，不要凭手感调参。

## 3. 金标准向量（两端共用）

`testdata/movement_golden.json` 新增字段：

| 字段 | 含义 |
|---|---|
| `blocked: [[x,y]]` | **硬墙格**（地形：水/悬崖），跨格不可走 → 贴边停在边界外侧 |
| `shapes: [{kind:"circle",x,y,r}]` | 格心圆（树/岩）：世界坐标已含 +0.5，半径单位=格 |
| `shapes: [{kind:"box",x,y,w,h}]` | 占格盒（建筑/工作站）：左上角锚点 + 尺寸 |
| `body_radius` | 移动体半径；有 `shapes` 时必填 |
| `tol` | 比较容差；缺省 1e-6，带接触回退的向量为 2e-3 |

用例覆盖：圆正面撞停（`shape_circle_head_on_stop`）、圆的偏心擦过（`shape_circle_offset_slide_around`）、
圆对角正撞（`shape_circle_diagonal_head_on`）、岩石正面（`shape_circle_rock_head_on`）、
圆旁掠过（`shape_circle_pass_beside`）、圆后有硬墙（`shape_circle_after_hard_wall`）、
盒正面撞停（`shape_box_head_on_stop`）、盒面斜推沿墙滑（`shape_box_face_slide_along`）、
盒角对角撞走墙角兜底（`shape_box_corner_slide_assist`）。

客户端 `MovementGoldenTests` 要按新字段改造：`blocked` 映射到 `_walkable`，
`shapes` 交给 `Slide`，数量与 `tol` 都从 JSON 读。

## 4. 验证

服务端：

```bash
make check      # fmt/mod/proto/build/test -race/lint/config-check
go test ./pkg/collide/ ./internal/game/systems/ ./internal/game/world/
```

客户端：

```bash
cp ../starve/testdata/movement_golden.json Starve.Core/
make check
STARVE_GATE_URL=ws://127.0.0.1:8081/ws make e2e
```

手感自检：贴树/贴墙停下时 `MovementDiagnostics.LastReconciliationError` 应稳定在 0.15 格以内；
若常态误差 0.3~0.5，多半是 `BodyRadius`、`Skin`、墙角兜底或"格子层在形状层之后提交"没对齐。

## 5. 参数一览（两端必须一致）

| 参数 | 值 | 来源 |
|---|---|---|
| 移动体半径 `BodyRadius` | 0.2 格 | 服务端 `internal/game/systems/move_step.go` |
| 接触回退 `Skin` | 1e-3 格 | `pkg/collide.DefaultSkin` |
| 最大接触次数 | 4 | `pkg/collide.DefaultSlideIterations` |
| 墙角兜底步长 | 0.1 格 | `pkg/collide.CornerProbeStep` |
| 树半径 | 0.103 格 | 客户端模型推导：`make model-collide`（[模型到碰撞体流水线](模型到碰撞体流水线.md)） |
| 岩石半径 | 0.28 格 | 同上（岩石在客户端是程序化基本体，走 fixed） |
| 占位寻路代价 | 圆 6 / 盒 1000 | `internal/game/worldmap/path.go` |
