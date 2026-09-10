# 碰撞检测可视化（Go → WebAssembly）

把 [`pkg/collide`](../../pkg/collide) 编译成 WebAssembly 在浏览器里跑，用于开发期自测，不依赖 C# 客户端。

三个页面：

- `index.html` — 基础图元：拖拽图形，实时看 Go 算出的最近点 / 距离 / 重叠（点–线段、点–三角形、点–OBB、胶囊–胶囊、球–AABB、球–OBB）。
- `scenes.html` — 命中判定场景：子弹射击（球扫掠一组 AABB/胶囊，命中处标红）、劈砍（**刀是 OBB**，按角度细分扫掠，命中胶囊身体）、劈砍隔墙（刀先砍在墙上，目标不受影响）。
- `sim.html` — 压力测试：几百个球在盒子里受重力下落、互相碰撞，接触点短暂标红，看复杂情况下碰撞检测是否稳定。

## 用法

```bash
make wasm-collide     # 编译 web/collide/collide.wasm 并拷贝 wasm_exec.js
make serve-collide    # 起本地 http 服务（默认 8099）
```

然后浏览器打开 <http://localhost:8099/>（命中场景 `/scenes.html`、掉落压测 `/sim.html`）。

> WASM 必须经 HTTP 加载，直接双击 `index.html`（file://）不行。

## 交互（基础图元页）

- 顶部按钮切换用例：点–线段 / 点–三角形 / 胶囊–胶囊 / 球–AABB
- 在画布上拖动红点移动对象（平面 x/z），滑杆调高度（y）
- 黄色点是由 Go 计算出的最近点，虚线是最近点连线
- 左下角 HUD 显示重叠状态与距离
- 拖空白处旋转 · 滚轮缩放 · 拖红点移动对象 · 滑杆调高度

## 交互（压力测试页）

- 滑杆调物体数量（10–300）与弹性，按钮重置
- 拖空白处旋转、滚轮缩放
- 红色圆点是本帧的接触位置，会短暂显示后淡出

## 交互（命中场景页）

- 顶部切换三种场景；「重放」重跑当前场景
- 子弹交给 Go 的 `collideCast`（球扫掠 AABB / 胶囊）；劈砍交给 `collideSwing`（刀是 OBB，按角度细分做旋转扫掠）
- 左下角 HUD 显示命中目标、接触点、沿运动比例 t 与距离

## 说明

- `collide.wasm` 与 `wasm_exec.js` 是构建产物，已在 `.gitignore` 中排除。
- 基础页：场景以 JSON 传给 Go 的 `collideEval`，结果以 JSON 返回。
- 命中场景页：子弹走 `collideCast`（`SweepSphereAABB` / `SweepSphereCapsule`）；劈砍走 `collideSwing`（`ContactOBBOBB` / `ContactCapsuleOBB`，角区间细分为保守前进）。
- 压力测试页：整场景每帧交给 Go 的 `collideSim` 步进一次（积分 → 平面约束 → 两两碰撞），碰撞判定全部走 `pkg/collide`。
- 坐标系：右手系，x 右 / z 上（俯视平面），y 为高度。
