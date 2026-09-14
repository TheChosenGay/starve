# pkg/collide · 碰撞检测库

纯 Go 的三维碰撞检测：**图元 + 几何查询 + 宽阶段引擎**。不用 cgo，不含任何游戏概念，
服务端、客户端、离线工具都能直接复用。

算法取自 Christer Ericson《Real-Time Collision Detection》（第 4/5 章）。

## 目录结构

```text
pkg/collide/
  primitive/     图元：向量、AABB / OBB / 球 / 胶囊 / 线段 / 三角形 / 射线 / 平面
                 ——只有数据与它自己的方法（向量运算、包围盒运算、Bounds()、Kind()）
  kind/          图元类型标识（编译期常量，零内存零开销）
  （本目录）      算法 + 引擎：
                   · 最近点 / 距离 / 重叠 / 接触（法向、深度、接触点）
                   · 射线相交、平移扫掠（防隧穿）
                   · 按类型双分发：TestShapes / ContactShapes / Raycast / SweepShapes
                   · 圆弧与扇形：ArcBounds / Sector（挥砍、技能锥的查询体积）
                   · 宽阶段：Scanner 接口 + ArrayScanner（线性）+ BVHScanner（动态 AABB 树）+ Engine
```

图元的定义在 `primitive`，`collide` 用**类型别名**重导出，所以两处名字都能用，
但只有一份定义（同标准库 `os.FileMode = fs.FileMode` 的手法）：

```go
var s primitive.Sphere   // 只做几何计算时用这个
var t collide.Sphere     // 等价，同一个类型
```

## 三层概念

| 概念 | 是什么 | 例子 | 会不会被别的查询命中 |
|---|---|---|---|
| **图元**（primitive） | 有体积、能给出紧包围盒 | `Sphere` `Capsule` `AABB` `OBB` `Triangle` | 会（注册进引擎后有句柄） |
| **查询体积** | 只用来"问"，不参与碰撞代数 | `Sector`（扇形）、`Ray`、扫掠盒 | 不会（临时构造） |
| **代理**（proxy） | 引擎里的一个句柄 + 它的 fat AABB | `Handle` | — |

查询体积可以是非凸的（扇形 `R0 > 0` 时是环形）而不引入任何几何代价，
正因为**它不需要参与 SAT / 最近点 / 接触法向这套凸图元代数**。

## 快速上手

### 1. 建引擎，注册图元（每个对象只做一次）

```go
e := collide.NewEngine(collide.EngineOptions{
    Margin:  0.25,   // fat AABB 外扩量：小幅移动不用动索引
    Scanner: nil,    // nil = 默认 ArrayScanner；以后可换 BVH 实现
})
e.Reserve(20000)

h := e.Add(collide.Capsule{          // 业务在这里声明一次"我是什么形状"
    A: collide.Vec3{X: x, Z: z},
    B: collide.Vec3{X: x, Y: 1.8, Z: z},
    R: 0.35,
})
```

### 2. 移动（每帧）

```go
body.A.X += dx
body.A.Z += dz
body.B.X += dx
body.B.Z += dz
e.Update(h, body)
```

`Update` 是**整体替换图元**，不是"平移"：图元一旦加入就当作不可变，
所有修改都走 `Update`。它幂等，而且新图元仍在旧 fat AABB 内时**索引一次都不会被碰**。

### 3. 查询：业务只传句柄与数字

```go
// 近战：这一刀扫到了谁，打在身上哪个位置
found := e.OverlapSector(collide.Sector{
    Center: playerPos, From: -0.8, To: 0.8, R0: 0.6, R1: weapon.Reach,
}, func(h collide.Handle) bool {
    return h != attacker          // 过滤自己；阵营等业务规则也写在这里
}, func(r collide.Result) bool {
    spawnBlood(r.Point, r.Normal) // 命中部位 + 击退方向
    return true
})

// 子弹 hitscan：打中谁、多远、命中点
hit, ok := e.Raycast(muzzle, dir, weapon.Range, nil)

// 弹丸：这一帧飞出去先撞到谁（扫掠，防隧穿）
hit, ok = e.SweepHit(collide.Sphere{C: bullet, R: 0.1}, vel.Scale(dt), nil)

// 形状重叠：命中回调拿到接触法向 / 深度 / 接触点
e.Overlap(collide.Sphere{C: pos, R: 3}, nil, func(r collide.Result) bool {
    return true
})
```

判定规则（阵营、无敌帧、冷却、伤害）写在回调里；几何判定全在库里。
判据很简单：**业务代码里出现 `AABB` / `OBB` / `Capsule` 这些词，就是分层漏了**。

## 引擎 API

```go
// 生命周期
func NewEngine(EngineOptions) *Engine
func (e *Engine) Reserve(n int)
func (e *Engine) Add(s Solid) Handle          // 返回句柄，之后一直用它
func (e *Engine) Update(h Handle, s Solid)    // 整体替换；在 fat AABB 内则不动索引
func (e *Engine) Remove(h Handle)             // 句柄随即失效（再用会 panic）
func (e *Engine) Rebuild()                    // 全量重建索引
func (e *Engine) Len() int
func (e *Engine) Has(h Handle) bool
func (e *Engine) Shape(h Handle) Solid
func (e *Engine) Box(h Handle) AABB           // 该代理的 fat AABB

// 查询
func (e *Engine) Query(box AABB, fn func(h Handle) bool)                  // 只做宽阶段，给候选
func (e *Engine) Overlap(q Solid, f Filter, fn func(r Result) bool) bool
func (e *Engine) OverlapSector(s Sector, f Filter, fn func(r Result) bool) bool
func (e *Engine) Raycast(o, dir Vec3, maxDist float64, f Filter) (Result, bool)
func (e *Engine) RaycastAll(o, dir Vec3, maxDist float64, f Filter) []Result
func (e *Engine) SweepHit(s Solid, motion Vec3, f Filter) (Result, bool)
func (e *Engine) SweepSlideSphere(s Sphere, motion Vec3, f Filter, o SlideOptions) SlideResult
func (e *Engine) SweepSlideCapsule(a, b Vec3, radius float64, motion Vec3, f Filter, o SlideOptions) SlideResult

type Filter func(h Handle) bool   // 返回 true 表示参与本次查询；nil = 全部参与

type Result struct {
    Handle Handle
    Point  Vec3    // 碰撞部位：图元表面上的点，直接可用于特效 / 贴花 / 溅血
    Normal Vec3
    Dist   float64 // 射线 / 扫掠的距离
    T      float64 // 扫掠比例 [0,1]
    Depth  float64 // 穿透深度
}
```

`Normal` 的语义按查询类型分：`Overlap` 是接触法向（从被查询方指向查询方）；
`OverlapSector` 是"从扇心指向命中点"，也就是击退方向；`Raycast` / `SweepHit` 是表面法向。

### 句柄为什么带代次

```go
type Handle uint64   // 低 32 位槽位、高 32 位代次
```

对象销毁后槽位会被复用，只用下标的话旧句柄会**悄悄指向新对象**（ABA），不崩不报错、
只是结果莫名其妙。`Remove` 时代次 +1，旧句柄随即失效并 panic——测试里立刻炸，
而不是线上给出诡异结果。

### 扫描器可换

```go
type Querier interface { Query(box AABB, fn func(h Handle) bool) }   // 只读能力
type Scanner interface {
    Querier
    Reset()
    Insert(h Handle, b AABB)
    Update(h Handle, b AABB)
    Remove(h Handle)
    Len() int
}
```

两个实现，互换只改一行 `EngineOptions.Scanner`：

| 实现 | 查询复杂度 | 每次查询的盒子测试 | 适用 |
|---|---|---|---|
| `ArrayScanner`（默认） | O(N) | N（线性过一遍） | 几百个代理；或作为对拍基准 |
| `BVHScanner` | O(log N + 命中数) | ~log N 个节点 + 相交子树 | 上千以上；需要频繁查询 |

数组扫描器不做空间加速，但已经把宽阶段最值钱的那一半做了——**先用极便宜的 AABB 判定剔除**
（盒子测试比形状测试便宜 20–100 倍）。BVH 再把"盒子测试本身"从线性降到对数级，
所以两者是叠加收益，不是替代关系。

```go
e := collide.NewEngine(collide.EngineOptions{
    Margin:  0.25,
    Scanner: collide.NewBVHScanner(),   // 不传就是 ArrayScanner
})
```

`BVHScanner` 是一个动态 AABB 树：节点存在切片里（下标而非指针引用）、空闲节点复用、
插入按"表面积增量最小"贪心选兄弟、删除用兄弟顶替父节点、refit 时做一次单旋转防退化，
`Build` 走自顶向下按最长轴中位数切分（O(N log N)、零分配）。`Visits` 字段可以读出
一次查询访问了多少节点，用来观测树的质量。

实测（本机 M4 Pro，32 个查询、半径 1.6 的球、1000×1000 米世界）：

| 物体数 | 不用宽阶段 | 数组扫描器 | BVH 扫描器 | BVH/数组 | BVH/不用 |
|---|---|---|---|---|---|
| 200 | 0.16 ms | 0.011 ms | 0.004 ms | 2.6× | 38× |
| 2 000 | 1.59 ms | 0.107 ms | 0.008 ms | 13× | 191× |
| 20 000 | 16.8 ms | 1.05 ms | 0.035 ms | 30× | 479× |
| 80 000 | — | 4.22 ms | 0.090 ms | 47× | — |

规模涨 400 倍（200 → 80 000），BVH 的查询耗时只涨 21 倍——这就是 O(log N) 与 O(N) 的分叉。
复现：`go test -run '^$' -bench 'BenchmarkBroadQuery' ./pkg/collide/`。

建树成本（`BenchmarkBVHBuildVsInsert`）：20 000 个代理，自顶向下 bulk build 10.2 ms
（树高 10），逐个 Insert 22.1 ms（树高 12）——既有速度也有质量。

## 宽阶段值不值

实测（M4 Pro，每对图元的成本）：

| 操作 | 成本 |
|---|---|
| AABB-AABB 剔除 | ~0.28 ns |
| 球-胶囊相交（窄阶段） | ~6 ns |
| 盒-盒接触（15 轴 SAT） | ~40 ns |
| 胶囊-盒接触 | ~75 ns |
| 球扫掠胶囊 | ~430 ns |

剔除比窄阶段便宜 20–100 倍，这就是宽阶段的全部意义。32 个查询的整批耗时
（数组扫描器 vs BVH 的详细对照见上面「扫描器可换」一节）：

复现：`go test -run '^$' -bench . ./pkg/collide/`；
浏览器里的对照演示见 [`web/collide/broad.html`](../../web/collide/broad.html)。

## 设计取舍

**fat AABB 是增量的全部意义。** 索引里存的是紧包围盒外扩 `Margin` 之后的盒子。
`Update` 先看新紧盒是否还在旧 fat 盒里，在就直接返回、索引一次都不碰。
`Margin` 是调用方的正确性参数：静态物 0，高速物体按单帧最大位移给。
库替你选默认值，等于把正确性参数藏起来。

**图元加入后不可变。** 调用方通常持有自己的图元切片；如果绕过 `Update` 直接改它，
索引里的 AABB 会静默过期，宽阶段随后给出的是**漏判**（不是错判，测试很难抓）。
规则只有一条：所有修改走 `Update`。

**未实现的组合要吵。** `TestShapes` / `ContactShapes` / `IntersectRayShape` / `SweepShapes`
遇到没实现的图元组合会 panic，而不是返回 false——静默 false 会让"零漏判"的承诺失效。

**顺序确定性。** `Query` 的回调顺序由扫描器实现决定，但候选集合与顺序在给定实现下是确定的
（数组扫描器 = 插入顺序，无 map 遍历）。需要"最近 / 最先"时由引擎内部比较 `T` / `Dist` 决定，
不依赖回调顺序。

**为什么用扇形而不是旋转扫掠体。** 刀身沿弧线扫过的体积一般是**非凸**的
（部分角度上是楔形，`R0 > 0` 时还带洞），进不了这套以凸性为前提的图元代数
（SAT、最近点、接触法向全都不成立）。扇形查询体积只需要一个包围盒 + 一个谓词，
非凸也无所谓；它的包围盒还是闭式精确的（内弧盒 ∪ 外弧盒，不采样）。
平移扫掠（子弹、角色移动）是凸的，仍然用闭式扫掠解。

**扇形可以绕任意轴张开。** `Sector` 的 `Axis` / `Ref` 决定它在哪个平面、角度 0 朝哪：

```go
// 水平挥砍 / 技能锥：绕 +Y，角度 0 = 朝向
collide.Sector{Center: pos, Ref: facing, From: -0.8, To: 0.8, R0: 0.6, R1: 2.6, Thickness: 3}

// 竖直劈砍：绕「竖直 × 朝向」，正角度朝上（举起）→ 负角度朝下（收刀）
collide.Sector{Center: chest, Axis: collide.YAxis.Cross(facing), Ref: facing,
    From: 1.1, To: -0.7, R0: 0.5, R1: 2.6, Thickness: 0.5}
```

`Thickness` 是垂直于扇形平面的半宽：水平挥砍给一个覆盖世界高度的值（俯视玩法常用 3–5 米），
竖直劈砍给武器厚度即可。**扇形在轴向上总是有界的**——"沿轴不设限"看着方便，
但那会让宽阶段退化成"全部返回"，也会打到不该打的东西。

**体积查询用探针判定，不是只看最近的一个点。** `EachProbe` 把目标拆成一组代表点
（胶囊沿中轴 5 个、盒取中心 + 8 角点、球取球心），任一点落在扇形里就算相交。
只探"离扇心最近的点"在水平挥砍里够用，但竖直劈砍打高个子时会漏：
那个点永远固定在胸口高度，看不出目标顶部/底部先进入刀路。

## 已实现 / 未实现

| 入口 | 支持的组合 |
|---|---|
| `TestShapes` | 球 × 球/胶囊/点/线段/三角形/盒/OBB；胶囊 × 胶囊/线段/盒/OBB；盒 × 盒/OBB；OBB × OBB；线段 × 线段 |
| `ContactShapes` | 球 × 球/胶囊/盒/OBB；胶囊 × 胶囊/盒/OBB；盒 × 盒/OBB；OBB × OBB |
| `IntersectRayShape` | 射线 × 球 / AABB / OBB |
| `SweepShapes` | 移动体为球，目标 ∈ 球 / AABB / OBB / 胶囊 / 三角形 |
| `SweepSlideSphere` | 移动体为球；沿接触切面滑动（上表全部目标类型，靠 `SweepShapes` 分发） |
| `SweepSlideCapsule` | 移动体为胶囊（段 + 半径）；按"沿轴采样球"复用上面的球版内核，语义一致 |

表里没有的组合一律 panic。缺口与后续方向统一列在下面的「后续改进方向」。

## 后续改进方向

按「现在就能做 / 需要新算法 / 结构性」三档，每项都写了为什么值得做、以及做的代价在哪。

### 近期：接口已就位，动手即可

**1. BVH 的树质量与重建策略（已完成第一版，这里是继续做的方向）**
`BVHScanner` 已经能用（贪心下降 + 单旋转 + 中位数 bulk build）。还可以：
插入用 Box2D 那种更完整的表面积代价模型（而不是只看"合并后增量"）、
按表面积（SAH）而不是最长轴中位数切分、删除后按需局部重构。
另外注意成本结构：数组扫描器的"全量重建"只是重写数组（比增量还便宜），
BVH 的重建是主要成本（20 000 个约 10 ms），所以 BVH 场景下 `Update` 的
"fat AABB 内不动索引"才真正值钱。

**2. 射线 × 胶囊**
hitscan 打"胶囊身体"目前只能退化成球或盒，这是最常见的缺口。
闭式解：无限圆柱求交（把射线投影到垂直于轴线的平面）+ 两端球帽，取最近的有效 t。

**3. 胶囊对静态物的扫掠 + 沿墙滑动（已落地，球体；胶囊移动体待补）**
`Engine.SweepSlideSphere` 就是这套内核：连续扫掠（SweptAABB 宽阶段 + `SweepShapes` 窄阶段）
→ 推进到接触点 → 沿法向回退 `Skin` → 把剩余位移投影到接触切面 → 迭代最多 4 次；
初始重叠（读档/传送落在障碍里）会按接触深度推出，所以能自愈；**完全被挡住时走墙角兜底**
（按 `CornerProbeStep=0.1` 离散推进 + 沿较近轴推出）——8 向输入正对角撞盒子的角
会顺着墙面滑开，而不是因为法向恰好与位移反向被钉死；正对平面推时仍按面法向推出，语义不变。
俯视玩法把移动体当平面圆
（`Sphere` 高度固定在角色身高内，障碍是竖直圆柱）用时，三维扫掠正好退化成 XZ 平面的圆-圆扫掠。
目标可以是圆柱（树/岩）也可以是轴对齐盒（建筑/墙），两者都在 `SweepShapes` 的支持范围内。

用法见 `internal/game/collision/world.go`（世界侧形状索引）与 `internal/game/systems/move_system.go`
（每 tick 先形状层滑动、再格子层 `stepAxis`）；金标准向量
`testdata/movement_golden.json` 里的 `shapes`/`body_radius` 用例锁定了它的输出。

同一条内核的**胶囊移动体**版本是 `Engine.SweepSlideCapsule`：`collision.World.SlideBody`
在 `Body.HalfLength > 0`（四足生物）时走它。胶囊按"沿轴采样球"实现——段上按间距 ≤ r
采样一串球，逐个复用 `SweepHit` 的球×形状窄阶段，取最早的 `t`；宽阶段先剔一次
（整段扫掠盒里没有候选就直接走完，绝大多数 tick 走这条）。偏差 < 4%·r（凸包络一致），
采样数上限 32。语义（连续扫掠 / 回退 `Skin` / 切面投影 / 4 次迭代 / 墙角兜底）与球版逐条一致。

还没做的部分：旋转扫掠体（旋转扫掠一般非凸，见下面的设计取舍）。

**4. 消掉 `Update` 的装箱分配**
传值类型图元进接口时会有一次堆分配（bench 里 2000 次更新 = 2000 次分配）。
两条路：图元分桶存储（`[]Sphere` / `[]Capsule` 各一份，句柄里带桶号），
或者让分发层同时接受指针类型（`*Sphere`），调用方就能传稳定切片里的地址。

### 中期：需要新算法

**5. GJK / EPA 作为"任意凸体"的兜底**
现在每加一种图元要写 O(N²) 个组合（`TestShapes` 的 switch 会越长越吓人）。
GJK 把"任意两个凸体"收敛成一个算法：新图元只要提供支持函数
（沿方向的最远点）就自动能参与判定，EPA 还能给出穿透深度与法向。
建议定位成**未实现组合的兜底**而不是替换现有实现——专门写的代码在接触点精度和速度上更好。

**6. 多点接触流形（contact manifold）**
现在每对图元只返回一个代表接触点。做刚体堆叠（箱子摞箱子）时单点接触会抖，
需要参考面裁剪出 1–4 个接触点。这是"判定库"走向"物理"的分水岭，不急但有代价要提前知道。

**7. 三角形相关的接触 + 凸包**
三角形现在只进了 `TestShapes` 和 `SweepShapes`，没有接触（法向/深度）。
地形网格、建筑多边形会需要；顺带把凸包（`ConvexHull`）加进来，
地形与建筑就能用同一套近似。

**8. 扇形的精确档**
现在引擎用 `EachProbe` 的探针做近似判定（胶囊沿中轴 5 点、盒取中心 + 8 角点），
绝大多数情况够用，但目标只有极小的一个角探进扇区时仍会漏。
要精确就得按图元求"形状到扇区的最近距离"——球和胶囊可以闭式
（扇区是圆环楔形，等价于求到两段圆弧与两条边的最近距离），盒要按顶点/边分类。

### 长期：结构性

**9. 并发只读查询**
如果以后想渲染线程/网络线程直接查世界，需要扫描器支持"读快照"或版本号校验：
写仍在模拟线程，读拿到一个不可变视图。当前设计是单线程独占，别在没做这件事之前跨线程用。

**10. 批量更新 API**
一帧内同一个句柄可能被移动多次（移动、击退、传送），每次都推一次索引是浪费。
`UpdateBatch` 或 dirty 表 + `Flush()`（只推最后一次）能省掉重复的索引操作。

**11. 查询结果零分配**
`RaycastAll` 每次调用都会分配结果切片。给引擎加一个可复用的缓冲
（`RaycastAllInto(buf []Result, ...) []Result`）就行，命中结果本身已经是值类型。

**12. fuzz 测试**
随机图元对 + 采样真值（相切、共面、退化成点/线这些边界最容易出错），
`go test -fuzz` 能兜住手工用例覆盖不到的角落。宽阶段的暴力对拍结构已经在了，
把随机源换成 fuzz 输入即可。

**13. 形状变换 API**
`Transform{R, T}` + 应用到图元（注意 AABB 旋转会**提升类型**成 OBB）。
有了它，"武器在局部坐标里定义、攻击时算世界位姿"就能自然表达，
将来若真需要刀身精确判定，`PoseAt` / 旋转扫掠体也有地方落——但要记住那条老问题：
旋转扫掠体一般是非凸的，进不了现在的凸图元代数。

## 测试与基准

```bash
go test ./pkg/collide/...                                   # 全部单测
go test -run '^$' -bench . ./pkg/collide/                   # 宽阶段基准
go test -run TestArcBoundsMatchesSampling -v ./pkg/collide/ # 闭式解 vs 稠密采样
```

几条值得知道的测试策略：

- **闭式解用稠密采样当真值**：`ArcBounds` 声称精确且不采样，就用几万个采样点对拍，
  既验证"不漏"也验证"不过松"。
- **宽阶段用暴力对拍**：`Engine.Overlap` 的命中集合必须与"遍历全部图元"完全一致；
  `ArrayScanner` 在增删改混合之后同样与暴力一致。
- **fat AABB 语义用计数扫描器验证**：包一层记录 `Insert`/`Update` 次数的扫描器，
  断言"还在 fat 盒里的移动一次索引操作都不产生"。
- **对称性**：`TestShapes` 必须与参数顺序无关；`ContactShapes` 交换参数后法向必须取反
  （两个图元完全重合的退化情形除外，测试里显式计数并允许）。

浏览器可视化见 [`web/collide/README.md`](../../web/collide/README.md)
（`make serve-collide` 之后打开 <http://localhost:8099/>）。
