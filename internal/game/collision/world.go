// Package collision 维护世界的静态碰撞索引，并提供角色移动用的"扫掠 + 沿切面滑动"。
//
// 与 worldmap.MapData 的分工：MapData 是格子层（地形可走 + 占位代价），格子层解决
// "这格能不能进、值不值得绕"；本包是形状层，解决"能贴到多近、撞上怎么滑"。
// 所有占位物都在形状层有体：树/岩是格心圆柱，建筑/工作站是占格盒——占位格仍然可走，
// 挡人的是形状。
package collision

import (
	"math"

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

// World 是世界的静态碰撞索引：实体 → 格心圆柱。
// 静态物不移动，所以 fat AABB 的外扩量 Margin 为 0（外扩只对会动的代理有意义）；
// 森林图有上千棵树，宽阶段用 BVH（查询 O(log N)）。
type World struct {
	eng     *collide.Engine
	handles map[ecs.Entity]collide.Handle
}

// NewWorld 建一个空索引。
func NewWorld() *World {
	return &World{
		eng:     collide.NewEngine(collide.EngineOptions{Margin: 0, Scanner: collide.NewBVHScanner()}),
		handles: make(map[ecs.Entity]collide.Handle),
	}
}

// Set 注册/更新一个实体的圆形碰撞体：中心 (centerX, centerY)（格，浮点）、半径 radius（格）。
// 半径 ≤ 0 或实体 id 为 0 时按注销处理。树干/岩石这类"占不满一格"的占位物用它。
func (c *World) Set(e ecs.Entity, centerX, centerY, radius float64) {
	if c == nil || e == 0 {
		return
	}
	if radius <= 0 {
		c.Clear(e)
		return
	}
	c.put(e, cylinder(centerX, centerY, radius))
}

// SetBox 注册/更新一个实体的盒状碰撞体：左上角 (x, y)、尺寸 width×height（格）。
// 建筑/工作站/城墙这类占整格的占位物用它。尺寸 ≤ 0 按注销处理。
func (c *World) SetBox(e ecs.Entity, x, y float64, width, height float64) {
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

// put 登记/替换实体名下的形状（句柄复用：实体身份不变，形状整体替换）。
func (c *World) put(e ecs.Entity, shape collide.Solid) {
	if h, ok := c.handles[e]; ok {
		c.eng.Update(h, shape)
		return
	}
	c.handles[e] = c.eng.Add(shape)
}

// Clear 注销一个实体的碰撞体；未注册的实体忽略（幂等）。
func (c *World) Clear(e ecs.Entity) {
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

// Reset 清空索引（世界构建/读档后按实体全量重建）。
func (c *World) Reset() {
	if c == nil {
		return
	}
	c.eng = collide.NewEngine(collide.EngineOptions{Margin: 0, Scanner: collide.NewBVHScanner()})
	clear(c.handles)
}

// Len 返回已注册的碰撞体数量。
func (c *World) Len() int {
	if c == nil {
		return 0
	}
	return c.eng.Len()
}

// Slide 让半径 radius 的圆从 (x, y) 沿 (dx, dy) 前进：连续扫掠保证不穿树，
// 撞上之后把剩余位移投影到接触切面继续滑。返回实际位移（长度 ≤ 请求位移）与接触次数。
// 索引为空时原样返回请求位移（零开销快路径）。
func (c *World) Slide(x, y, dx, dy, radius float64) (float64, float64, int) {
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
func (c *World) SlideBody(b Body, dx, dy float64) (float64, float64, int) {
	if c == nil || c.eng.Len() == 0 || b.Radius <= 0 {
		return b.X + dx, b.Z + dy, 0
	}
	motion := collide.Vec3{X: dx, Z: dy}
	if b.HalfLength <= 0 {
		mover := collide.Sphere{C: collide.Vec3{X: b.X, Y: moverPlaneY, Z: b.Z}, R: b.Radius}
		res := c.eng.SweepSlideSphere(mover, motion, nil, collide.SlideOptions{Skin: slideSkin})
		end := mover.C.Add(res.Moved)
		return end.X, end.Z, res.Hits
	}
	// 胶囊：段沿朝向铺开，中心仍在参考点
	fx, fz := b.FaceX, b.FaceZ
	if fx == 0 && fz == 0 {
		fx, fz = 1, 0 // 朝向未知时按 +X（胶囊是对称的，只影响朝向不影响长短）
	}
	norm := math.Hypot(fx, fz)
	fx, fz = fx/norm, fz/norm
	a := collide.Vec3{X: b.X - fx*b.HalfLength, Y: moverPlaneY, Z: b.Z - fz*b.HalfLength}
	bb := collide.Vec3{X: b.X + fx*b.HalfLength, Y: moverPlaneY, Z: b.Z + fz*b.HalfLength}
	res := c.eng.SweepSlideCapsule(a, bb, b.Radius, motion, nil, collide.SlideOptions{Skin: slideSkin})
	center := a.Add(bb).Scale(0.5).Add(res.Moved)
	return center.X, center.Z, res.Hits
}

// cylinder 把「格心圆」变成竖直圆柱（胶囊）：俯视玩法里圆柱就是平面圆。
func cylinder(centerX, centerY, radius float64) collide.Capsule {
	return collide.Capsule{
		A: collide.Vec3{X: centerX, Y: 0, Z: centerY},
		B: collide.Vec3{X: centerX, Y: SolidHeight, Z: centerY},
		R: radius,
	}
}
