// Package collision 维护世界的静态碰撞索引（Index），并提供角色移动用的"扫掠 + 沿切面滑动"。
//
// 与 worldmap.MapData 的分工：MapData 是格子层（地形可走 + 占位代价），格子层解决
// "这格能不能进、值不值得绕"；本包是形状层，解决"能贴到多近、撞上怎么滑"。
// 所有占位物都在形状层有体：树/岩是格心圆柱，建筑/工作站是占格盒——占位格仍然可走，
// 挡人的是形状。
//
// 本包的核心类型是 Index（不是 World）：它只是"障碍物登记册"，宿主是整个 ecs.World。
package collision

import (
	"math"
	"slices"

	"starve/internal/ecs"
	"starve/pkg/collide"
)

// moverPlaneY 是移动体扫掠时所在的高度平面。
// 玩法是俯视平面（XZ），Y 只用来区分高度：只要移动体与障碍在这个高度上都存在，
// 三维扫掠就退化成 XZ 平面上的圆-圆扫掠。树/岩的圆柱都从地面起算（见 SolidHeight）。
const moverPlaneY = 1.0

// SolidHeight 是形状碰撞体的高度（格）。角色身高在这个高度内与它相交即可，
// 所以取值只需大于 moverPlaneY，几何上不影响 XZ 平面上的判定。
// 导出给调试渲染用（客户端按这个高度画占位物的碰撞体）。
const SolidHeight = 3.0

// slideSkin 是滑动时每次接触的回退距离（格）：避免与障碍面共面后卡死。
const slideSkin = 1e-3

// Index 是世界碰撞体的空间索引：实体 → 形状（格心圆柱 / 占格盒 / 移动体胶囊）。
//
// 静态体与动态体放在**同一个引擎**里，用各自的 fat AABB 余量区分：
//   - 静态（树/岩/建筑/工作站）：margin = 0，永不移动，随世界构建/读档整体重建（Reset）；
//   - 动态（玩家/动物这类带 Moveable 的实体）：margin = DynamicMargin，每 tick 更新位置，
//     **不参与 Reset**（读档不该把动物形状清掉）。
//
// 为什么放同一个引擎而不是两个：扫掠滑动的几何解算（连续扫掠 + 沿切面投影 + 墙角兜底 +
// 起点重叠自愈）是一份很讲究的实现，放在 pkg/collide 里且已有金标准与回归。
// 分成两个引擎就必须自己再写一遍"多层轮询取最早接触"的循环——那样极易写出
// "静态层滑完把剩余位移交给动态层、结果被当成畅通而穿墙"这类 bug。
// 同一个引擎 = 一套解算、一份语义，动静只是 fat AABB 余量不同（见 Engine.UpdateWithMargin）。
//
// 为什么不叫 World：本包叫 collision，里面再放一个 World 会和 ECS 的 ecs.World
// 撞名——真实结构是"ecs.World（整个世界）上挂着一个 collision.Index（障碍登记册）"。
type Index struct {
	eng *collide.Engine
	// handles：实体 → 句柄（静态）。
	handles map[ecs.Entity]collide.Handle
	// dynHandles：实体 → 句柄（动态）。单独记一份，只为 Reset 时跳过它们。
	dynHandles map[ecs.Entity]collide.Handle
	// entByHandle：句柄 → 实体（动态）反查表，供 Neighbors 用（避免每次重建 map）。
	entByHandle map[collide.Handle]ecs.Entity
	// nbBuf 是 Neighbors 的复用缓冲（调用方须在下一次查询前用完）。
	nbBuf []Neighbor
}

// DynamicMargin 是动态体 fat AABB 的外扩量（格）。
// 取值权衡：太小 → 每 tick 移动都触发宽阶段重排；太大 → 宽阶段多报（窄阶段再筛掉）。
// 单 tick 最大位移 = 速度上限 × tick 时长：玩家 10 格/秒 × 50ms = 0.5 格。
// 取 1 格留一倍余量，让"走一大步"仍落在旧 fat AABB 内（此时 Update 完全不碰索引）。
const DynamicMargin = 1.0

// NewIndex 建一个空索引。
func NewIndex() *Index {
	return &Index{
		eng:         collide.NewEngine(collide.EngineOptions{Margin: 0, Scanner: collide.NewBVHScanner()}),
		handles:     make(map[ecs.Entity]collide.Handle),
		dynHandles:  make(map[ecs.Entity]collide.Handle),
		entByHandle: make(map[collide.Handle]ecs.Entity),
	}
}

// Set 注册/更新一个实体的圆形碰撞体（静态）：中心 (centerX, centerY)（格，浮点）、半径 radius（格）。
// 半径 ≤ 0 或实体 id 为 0 时按注销处理。树干/岩石这类"占不满一格"的占位物用它。
func (c *Index) Set(e ecs.Entity, centerX, centerY, radius float64) {
	if c == nil || e == 0 {
		return
	}
	if radius <= 0 {
		c.Clear(e)
		return
	}
	c.put(e, cylinder(centerX, centerY, radius))
}

// SetBox 注册/更新一个实体的盒状碰撞体（静态）：左上角 (x, y)、尺寸 width×height（格）。
// 建筑/工作站/城墙这类占整格的占位物用它。尺寸 ≤ 0 按注销处理。
func (c *Index) SetBox(e ecs.Entity, x, y float64, width, height float64) {
	if c == nil || e == 0 {
		return
	}
	if width <= 0 || height <= 0 {
		c.Clear(e)
		return
	}
	c.put(e, collide.AABB{
		Min: collide.Vec3{X: x, Y: 0, Z: y},
		Max: collide.Vec3{X: x + width, Y: SolidHeight, Z: y + height},
	})
}

// put 登记/替换实体名下的静态形状（句柄复用：实体身份不变，形状整体替换）。
func (c *Index) put(e ecs.Entity, shape collide.Solid) {
	if h, ok := c.handles[e]; ok {
		c.eng.Update(h, shape)
		return
	}
	c.handles[e] = c.eng.Add(shape)
}

// Clear 注销一个静态碰撞体；未注册的实体忽略（幂等）。
func (c *Index) Clear(e ecs.Entity) {
	if c == nil {
		return
	}
	h, ok := c.handles[e]
	if !ok {
		return
	}
	c.eng.Remove(h)
	delete(c.handles, e)
}

// SetDynamic 注册/更新一个**动态**碰撞体的胶囊形状（移动实体：玩家/动物）。
//
// 与静态体的区别只有两点：
//   - fat AABB 余量用 DynamicMargin（移动不会频繁触发宽阶段重排）；
//   - **不参与 Reset**（读档/换图重建静态层时不会被清掉）。
//
// 半径 ≤ 0 或实体 id 为 0 按注销处理。halfLength > 0 是沿 (faceX,faceY) 铺开的胶囊，
// = 0 是圆柱（人物）；朝向未知时按 +X（胶囊对称，只影响朝向不影响长短）。
func (c *Index) SetDynamic(e ecs.Entity, centerX, centerY, radius, halfLength, faceX, faceY float64) {
	if c == nil || e == 0 {
		return
	}
	if radius <= 0 {
		c.ClearDynamic(e)
		return
	}
	shape := collide.Solid(capsuleAt(centerX, centerY, radius, halfLength, faceX, faceY))
	if h, ok := c.dynHandles[e]; ok {
		c.eng.UpdateWithMargin(h, shape, DynamicMargin)
		return
	}
	h := c.eng.AddWithMargin(shape, DynamicMargin)
	c.dynHandles[e] = h
	c.entByHandle[h] = e
}

// ClearDynamic 注销一个动态碰撞体；未注册的实体忽略（幂等）。
func (c *Index) ClearDynamic(e ecs.Entity) {
	if c == nil {
		return
	}
	h, ok := c.dynHandles[e]
	if !ok {
		return
	}
	c.eng.Remove(h)
	delete(c.dynHandles, e)
	delete(c.entByHandle, h)
}

// Reset 清空**静态**索引（世界构建/读档后按实体全量重建）。
// 动态层不受影响：读档不改变"当前谁站在哪"，动物形状由每 tick 的同步维护。
func (c *Index) Reset() {
	if c == nil {
		return
	}
	for e, h := range c.handles {
		c.eng.Remove(h)
		delete(c.handles, e)
	}
	clear(c.handles)
}

// Len 返回已注册的静态碰撞体数量。
func (c *Index) Len() int { return len(c.handles) }

// DynamicLen 返回已注册的动态碰撞体数量。
func (c *Index) DynamicLen() int { return len(c.dynHandles) }

// TotalLen 返回引擎里的代理总数（静态 + 动态）。
func (c *Index) TotalLen() int {
	if c == nil {
		return 0
	}
	return c.eng.Len()
}

// Slide 让半径 radius 的圆从 (x, y) 沿 (dx, dy) 前进：连续扫掠保证不穿树，
// 撞上之后把剩余位移投影到接触切面继续滑。返回实际位移（长度 ≤ 请求位移）与接触次数。
// 索引为空时原样返回请求位移（零开销快路径）。
func (c *Index) Slide(x, y, dx, dy, radius float64) (float64, float64, int) {
	return c.SlideBody(Body{X: x, Z: y, Radius: radius}, dx, dy)
}

// Body 是移动体的碰撞形状：参考点 (X, Z) + 半径；HalfLength > 0 时是沿朝向铺开的胶囊
// （四足：长宽刚好包住模型），= 0 时是圆柱（人物/树）。
// FaceX/FaceZ 给轴向（不要求归一化）；HalfLength = 0 时忽略。
type Body struct {
	X, Z       float64
	Radius     float64
	HalfLength float64
	FaceX      float64
	FaceZ      float64
}

// SlideBody 让移动体沿 (dx, dy) 前进并沿接触切面滑动（圆柱走球扫掠，胶囊走段扫掠）。
// 索引为空/半径非法时原样返回请求位移（零开销快路径）。
// self = 0（不排除自己）：纯静态查询/测试用；移动实体请用 SlideBodyExcept。
func (c *Index) SlideBody(b Body, dx, dy float64) (float64, float64, int) {
	return c.SlideBodyExcept(b, dx, dy, 0)
}

// SlideBodyExcept 是带 self 排除的版本：移动实体传自己的 id。
//
// 动态体也在同一个引擎里，所以移动体自己的形状也在索引中；不排除就会"撞上自己"
// （起点即重叠 → 被自己推着走/卡死）。filter 按句柄比较，只排掉自己那一个。
func (c *Index) SlideBodyExcept(b Body, dx, dy float64, self ecs.Entity) (float64, float64, int) {
	if c == nil || c.eng.Len() == 0 || b.Radius <= 0 {
		return b.X + dx, b.Z + dy, 0
	}
	var filter collide.Filter
	if self != 0 {
		if h, ok := c.dynHandles[self]; ok {
			filter = func(hh collide.Handle) bool { return hh != h }
		}
	}
	motion := collide.Vec3{X: dx, Z: dy}
	if b.HalfLength <= 0 {
		mover := collide.Sphere{C: collide.Vec3{X: b.X, Y: moverPlaneY, Z: b.Z}, R: b.Radius}
		res := c.eng.SweepSlideSphere(mover, motion, filter, collide.SlideOptions{Skin: slideSkin})
		end := mover.C.Add(res.Moved)
		return end.X, end.Z, res.Hits
	}
	fx, fz := b.FaceX, b.FaceZ
	if fx == 0 && fz == 0 {
		fx, fz = 1, 0
	}
	norm := math.Hypot(fx, fz)
	fx, fz = fx/norm, fz/norm
	a := collide.Vec3{X: b.X - fx*b.HalfLength, Y: moverPlaneY, Z: b.Z - fz*b.HalfLength}
	bb := collide.Vec3{X: b.X + fx*b.HalfLength, Y: moverPlaneY, Z: b.Z + fz*b.HalfLength}
	res := c.eng.SweepSlideCapsule(a, bb, b.Radius, motion, filter, collide.SlideOptions{Skin: slideSkin})
	center := a.Add(bb).Scale(0.5).Add(res.Moved)
	return center.X, center.Z, res.Hits
}

// capsuleAt 把「中心 + 半径 + 朝向」变成竖直胶囊（俯视玩法里圆柱就是平面圆）。
// halfLength = 0 时退化成单点，即圆柱。
func capsuleAt(centerX, centerY, radius, halfLength, faceX, faceZ float64) collide.Capsule {
	fx, fz := faceX, faceZ
	if halfLength > 0 && fx == 0 && fz == 0 {
		fx, fz = 1, 0
	}
	if fx != 0 || fz != 0 {
		norm := math.Hypot(fx, fz)
		fx, fz = fx/norm, fz/norm
	}
	return collide.Capsule{
		A: collide.Vec3{X: centerX - fx*halfLength, Y: moverPlaneY, Z: centerY - fz*halfLength},
		B: collide.Vec3{X: centerX + fx*halfLength, Y: moverPlaneY, Z: centerY + fz*halfLength},
		R: radius,
	}
}

// cylinder 把「格心圆」变成竖直圆柱（胶囊）：俯视玩法里圆柱就是平面圆。
func cylinder(centerX, centerY, radius float64) collide.Capsule {
	return collide.Capsule{
		A: collide.Vec3{X: centerX, Y: 0, Z: centerY},
		B: collide.Vec3{X: centerX, Y: SolidHeight, Z: centerY},
		R: radius,
	}
}

// DynHandlesForTest 暴露动态句柄表（调试/测试用）。
func (c *Index) DynHandlesForTest() map[ecs.Entity]collide.Handle {
	out := make(map[ecs.Entity]collide.Handle, len(c.dynHandles))
	for k, v := range c.dynHandles {
		out[k] = v
	}
	return out
}

// --- 按运动类别分侧的查询（三阶段移动用）---

// Neighbor 是动态邻居的一个快照（ORCA 阶段用）。
type Neighbor struct {
	Entity     ecs.Entity // 邻居实体 id（调用方可用它取速度等）
	X, Z       float64    // 当前位置（格）
	Radius     float64    // 截面半径（格）
	HalfLength float64    // 胶囊半长（0 = 圆柱）
	FaceX      float64    // 轴向（未归一化）
	FaceZ      float64
}

// FilterStatic 返回一个只放行**静态**体的过滤器。
// 阶段②（静态碰撞）用它：扫掠时只看树/岩/建筑，不被别人的身体挡住——
// 动态体之间的避让是阶段③（ORCA）的职责，不是硬碰撞。
//
// 用句柄集合做 O(1) 判定（宽阶段每个候选都要过一次滤，不能是线性扫描）。
func (c *Index) FilterStatic() collide.Filter {
	return c.FilterStaticExcept(0)
}

// FilterStaticExcept 同上，并额外排除指定实体。
func (c *Index) FilterStaticExcept(self ecs.Entity) collide.Filter {
	if c == nil {
		return nil
	}
	dynSet := make(map[collide.Handle]struct{}, len(c.dynHandles))
	for _, h := range c.dynHandles {
		dynSet[h] = struct{}{}
	}
	selfH, hasSelf := c.dynHandles[self]
	return func(h collide.Handle) bool {
		if _, isDyn := dynSet[h]; isDyn {
			return false
		}
		if hasSelf && h == selfH {
			return false
		}
		return true
	}
}

// SlideStatic 是**阶段②**：只对静态体做"扫掠 + 沿接触切面滑动"，返回实际位移。
//
// 与 SlideBodyExcept 的区别：它把动态体全部过滤掉。这正是三阶段设计的关键——
// 静态用硬约束（绝不穿模），动态用 ORCA 软避让，两者不混在一次解算里
// （混在一起就是之前相向振荡的根因）。
func (c *Index) SlideStatic(b Body, dx, dy float64) (float64, float64, int) {
	if c == nil || c.eng.Len() == 0 || b.Radius <= 0 {
		return b.X + dx, b.Z + dy, 0
	}
	return c.slideWithFilter(b, dx, dy, c.FilterStatic())
}

// slideWithFilter 是 SlideBodyExcept 的共享实现，接受任意过滤器。
func (c *Index) slideWithFilter(b Body, dx, dy float64, filter collide.Filter) (float64, float64, int) {
	motion := collide.Vec3{X: dx, Z: dy}
	if b.HalfLength <= 0 {
		mover := collide.Sphere{C: collide.Vec3{X: b.X, Y: moverPlaneY, Z: b.Z}, R: b.Radius}
		res := c.eng.SweepSlideSphere(mover, motion, filter, collide.SlideOptions{Skin: slideSkin})
		end := mover.C.Add(res.Moved)
		return end.X, end.Z, res.Hits
	}
	fx, fz := b.FaceX, b.FaceZ
	if fx == 0 && fz == 0 {
		fx, fz = 1, 0
	}
	norm := math.Hypot(fx, fz)
	fx, fz = fx/norm, fz/norm
	a := collide.Vec3{X: b.X - fx*b.HalfLength, Y: moverPlaneY, Z: b.Z - fz*b.HalfLength}
	bb := collide.Vec3{X: b.X + fx*b.HalfLength, Y: moverPlaneY, Z: b.Z + fz*b.HalfLength}
	res := c.eng.SweepSlideCapsule(a, bb, b.Radius, motion, filter, collide.SlideOptions{Skin: slideSkin})
	center := a.Add(bb).Scale(0.5).Add(res.Moved)
	return center.X, center.Z, res.Hits
}

// Neighbors 收集 (x,z) 半径 searchRadius 内的**动态**邻居（阶段③ ORCA 的输入）。
// self 会被排除（ORCA 不该把自己当邻居）。结果按实体 id 排序，保证确定性。
func (c *Index) Neighbors(x, z, searchRadius float64, self ecs.Entity) []Neighbor {
	if c == nil || c.eng.Len() == 0 || searchRadius <= 0 {
		return nil
	}
	// 用 (handle, found) 而不是拿 0 当"没找到"的哨兵：
	// 句柄 0 完全合法（第一个 Add 出来的就是它）。早期版本用 selfH==0 表示"没找到"，
	// 恰好把实体 1 漏掉，于是它把自己当成了邻居，ORCA 被自己的约束压住 → 几乎走不动。
	selfH, selfFound := collide.Handle(0), false
	if h, ok := c.dynHandles[self]; ok {
		selfH, selfFound = h, true
	}
	// entByHandle 是动态体句柄 → 实体的反查表，由 SetDynamic/ClearDynamic 维护。
	// （早期版本每次查询都重建整个 map，是 O(动态体数) 的分配 + 哈希，
	//   实测 100 个实体就要 11.5µs——比查 20000 个静态障碍还贵。）
	byHandle := c.entByHandle

	q := collide.Sphere{C: collide.Vec3{X: x, Y: moverPlaneY, Z: z}, R: searchRadius}
	out := c.nbBuf[:0]
	c.eng.Overlap(q, nil, func(r collide.Result) bool {
		h := r.Handle
		if selfFound && h == selfH {
			return true // 自己不是自己的邻居
		}
		e, ok := byHandle[h]
		if !ok {
			return true // 静态体：不属于 ORCA 的邻居集合
		}
		out = append(out, c.neighborOf(e, c.eng.Shape(h)))
		return true
	})
	// 确定性：按实体 id 排序（ORCA 的解会依赖邻居顺序，必须固定）。
	// 用 slices.SortFunc 而不是 sort.Slice（后者走反射）。
	slices.SortFunc(out, func(a, b Neighbor) int {
		switch {
		case a.Entity < b.Entity:
			return -1
		case a.Entity > b.Entity:
			return 1
		}
		return 0
	})
	c.nbBuf = out
	return out
}

// neighborOf 从形状还原出邻居描述（位置/半径/朝向）。
func (c *Index) neighborOf(e ecs.Entity, shape collide.Solid) Neighbor {
	n := Neighbor{Entity: e}
	switch s := shape.(type) {
	case collide.Capsule:
		n.X = (s.A.X + s.B.X) / 2
		n.Z = (s.A.Z + s.B.Z) / 2
		n.Radius = s.R
		half := math.Hypot(s.B.X-s.A.X, s.B.Z-s.A.Z) / 2
		n.HalfLength = half
		if half > 0 {
			n.FaceX = (s.B.X - s.A.X) / (2 * half)
			n.FaceZ = (s.B.Z - s.A.Z) / (2 * half)
		}
	case collide.Sphere:
		n.X, n.Z, n.Radius = s.C.X, s.C.Z, s.R
	case collide.AABB:
		n.X = (s.Min.X + s.Max.X) / 2
		n.Z = (s.Min.Z + s.Max.Z) / 2
		n.Radius = math.Max(s.Max.X-s.Min.X, s.Max.Z-s.Min.Z) / 2
	}
	return n
}

// ScannerForTest 暴露底层 BVH 扫描器（观测访问节点数用）。
func (c *Index) ScannerForTest() *collide.BVHScanner {
	if c == nil || c.eng == nil {
		return nil
	}
	s, _ := c.eng.Scanner().(*collide.BVHScanner)
	return s
}
