package systems

import (
	"math"
	"sort"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// 爆炸的**几何判定**：向上半球与目标竖直区间的相交测试。
//
// # 为什么是半球
//
// 爆炸发生在**地面**（炸弹落地/炸药桶），能量向上扩散。用整球会把
// "地下的东西"也算进去；用水平圆则丢掉了高度信息（贴着地面的和
// 四层楼高的都同样受影响）。
//
// 现有数据刚好够算真半球：
//   - 爆心 = 落点的**地面高度**（客户端有地形高度，服务端用 0
//     作地面基准——模拟层本来就没有 Z 轴）；
//   - 目标 = 一个**竖直区间** [ground, ground+BodyHeight]，
//     因为 Collide 组件本来就带 BodyHeight（注释写明"移动只用水平
//     截面，这个字段供调试渲染用"——也就是说它此前从未参与模拟，
//     爆炸是第一个真正用它的地方）。
//
// 判定 = 区间与半球的**竖直重叠** ∧ **水平距离在半径内**：
//
//	设目标下沿 y0 = ground(target)、上沿 y1 = y0 + BodyHeight，
//	爆心在 y=0（地面），半径 R。
//	水平距离 d。目标在半球内 ⇔ 存在 y ∈ [y0,y1] 使 d² + y² ≤ R²
//	                        ⇔ d² + clamp(0, y0, y1)² ≤ R²
//	（因为 y≥0 时 y² 单调，取区间内**最接近 0** 的 y 即可）
//
// 这个式子同时覆盖三种情形：贴地目标（y0=0）、悬空目标（y0>0）、
// 以及"上半身探进爆炸球"的高个子。
//
// # 诚实说明：BodyHeight 目前对判定结果**没有实际影响**
//
// 因为 Position 只有 X/Y（没有 Z），所有目标的 ground 都是 0 →
// y=0 总落在区间 [0, BodyHeight] 内 → nearest 恒为 0 → 身高无关。
// 变异测试证实：把 bodyHeight 强制置 0，所有测试照样通过（等价变异体）。
//
// 保留这段代码的理由：一旦将来给 Position 加了 Z（悬空/高台/飞行），
// 判定**自动**就是正确的，不需要回来重写。现在它是"为未来准备好的正确公式"，
// 而不是"当前必需的复杂度"——写清楚以免后人以为它在起作用。
// 真有悬空需求时，本文件与 BodyHeight 都已就位。
func blastAffects(centerX, centerY, radius float64, tp components.Position, ground, bodyHeight float64) bool {
	if radius <= 0 {
		return false
	}
	dx := float64(tp.X) - centerX
	dy := float64(tp.Y) - centerY
	horiz2 := dx*dx + dy*dy
	// 目标竖直区间内**离爆心（y=0）最近**的高度
	y0 := ground
	y1 := ground + math.Max(0, bodyHeight)
	nearest := 0.0
	if y0 > 0 {
		nearest = y0 // 整个身体都在地面之上
	} else if y1 < 0 {
		nearest = y1 // 整体在地下（当前不会有）
	}
	return horiz2+nearest*nearest <= radius*radius
}

// BlastHit 是一次爆炸命中的一个目标及其受力。
type BlastHit struct {
	Entity ecs.Entity
	// Distance 水平距离（格），用于计算击退强度衰减。
	Distance float64
}

// BlastTargets 返回爆炸（中心 + 半径）能影响到的目标，按实体 id 升序。
//
// 只做**筛选**，不改任何状态——伤害与击退由调用方决定怎么施加。
// 这样测试可以单独验证几何，不必构造完整的伤害链路。
func BlastTargets(w *ecs.World, centerX, centerY, radius float64) []BlastHit {
	var out []BlastHit
	ecs.Query2[components.Position, components.Health](w, func(e ecs.Entity, p *components.Position, hp *components.Health) {
		if hp.Cur <= 0 || !w.IsAlive(e) || ecs.Has[components.Dead](w, e) {
			return
		}
		ground, bodyHeight := 0.0, 0.0
		if ecs.Has[components.Collide](w, e) {
			c := ecs.Get[components.Collide](w, e)
			bodyHeight = c.BodyHeight
		}
		if !blastAffects(centerX, centerY, radius, *p, ground, bodyHeight) {
			return
		}
		dx := float64(p.X) - centerX
		dy := float64(p.Y) - centerY
		out = append(out, BlastHit{Entity: e, Distance: math.Hypot(dx, dy)})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Entity < out[j].Entity })
	return out
}

// BlastKnockback 把目标沿"爆心 → 目标"方向推开。
//
// 强度按距离**线性衰减**：贴脸的最强，边缘趋近 0。
// 推进距离 = maxKnockback × (1 - d/R)。
//
// 为什么不直接改 Position：位移必须走**碰撞求解**，否则会把实体推穿墙。
// 这里复用移动系统的静态滑动（SlideBody），与玩家移动同一套约束——
// 少了它，一次爆炸就能把怪推进石头里。
//
// 返回实际位移（格），供测试与表现层使用。
func BlastKnockback(
	w *ecs.World,
	centerX, centerY float64,
	radius, maxKnockback float64,
	hit BlastHit,
) (float64, float64) {
	if maxKnockback <= 0 || radius <= 0 {
		return 0, 0
	}
	falloff := 1 - hit.Distance/radius
	if falloff <= 0 {
		return 0, 0
	}
	dist := maxKnockback * falloff

	// 方向：爆心 → 目标。距离为 0（正中心）时没有方向可推，
	// 用一个确定性的默认方向（+X），避免随机导致回放不一致。
	dx := float64(0)
	dy := float64(0)
	if ecs.Has[components.Position](w, hit.Entity) {
		p := ecs.Get[components.Position](w, hit.Entity)
		dx = float64(p.X) - centerX
		dy = float64(p.Y) - centerY
	}
	l := math.Hypot(dx, dy)
	if l < 1e-6 {
		dx, dy, l = 1, 0, 1
	}
	stepX := dx / l * dist
	stepY := dy / l * dist

	if !ecs.Has[components.Position](w, hit.Entity) {
		return 0, 0
	}
	p := ecs.Get[components.Position](w, hit.Entity)
	var col *components.Collide
	if ecs.Has[components.Collide](w, hit.Entity) {
		col = ecs.Get[components.Collide](w, hit.Entity)
	}
	// 用与本文件同包的滑移求解：推不动就沿切面滑，绝不穿墙。
	body := BodyOf(float64(p.X), float64(p.Y), col)
	endX, endY := body.X, body.Z
	if idx, ok := ecs.TryResource[collision.Index](w); ok {
		endX, endY, _ = idx.SlideStatic(body, stepX, stepY)
	} else {
		endX, endY = body.X+stepX, body.Z+stepY
	}
	movedX := endX - body.X
	movedY := endY - body.Z
	if movedX == 0 && movedY == 0 {
		return 0, 0
	}
	ApplyDisplacement(w, p, ecs.Ensure[components.Moveable](w, hit.Entity), components.MoveDir{}, endX, endY)
	return movedX, movedY
}
