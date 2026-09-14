package systems

import (
	"math"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
)

// BodyRadius 是移动体的**缺省**碰撞半径（格）：实体没带自己的碰撞体时用它。
// 目前的值 = 客户端玩家模型的躯干半径（configs/models.json 里 player 那条推导：
// pigman 躯干 0.305 格）。客户端预测必须用同一个值，否则贴近树/墙时会来回校正。
// 按实体给半径走 Moveable.BodyRadius（生物模板 body_radius）。
const BodyRadius = 0.305

// BodyHeight 是移动体的缺省身高（格，仅调试渲染用）：同样来自玩家模型推导（pigman 1.546 格）。
const BodyHeight = 1.546

// SlideStep 是移动里"碰撞 + 侧向移动"的独立入口：把这一 tick 的位移交给形状层，
// 撞到占位物（树/岩的格心圆、建筑的占格盒）就把剩余位移投影到接触切面继续滑，
// 返回实际发生的位移（方向可能已经变了）与接触次数。
// body 给出移动体形状：圆柱（HalfLength=0）或沿 FaceX/FaceZ 铺开的胶囊（四足）。
//
// 语义（与 pkg/collide.SweepSlideSphere 一致）：
//   - 连续扫掠，高速也不会穿过障碍；
//   - 每次接触沿法向退出 skin 再滑，避免贴面抖动/卡死；
//   - 起点就重叠（读档/传送落在障碍里）时按接触深度推出，能自愈；
//   - 完全被挡住（8 向输入正对角撞盒子的角）时走墙角兜底：离散推进 + 沿较近轴推出，
//     顺着墙面滑开而不是钉死在角上；正对平面推仍是停住；
//   - 最多 4 次接触消解，用不完的位移宁可丢掉也不穿墙。
//
// 没有占位物（或世界没注入碰撞索引）时是零开销直通，位移原样返回。
func SlideStep(w *ecs.World, body collision.Body, stepX, stepY float64) (float64, float64, int) {
	cw, ok := ecs.TryResource[collision.World](w)
	if !ok {
		return stepX, stepY, 0
	}
	endX, endY, hits := cw.SlideBody(body, stepX, stepY)
	return endX - body.X, endY - body.Z, hits
}

// MoveBody 推进一次位移：碰撞 + 侧滑（SlideStep）→ 格子层逐轴提交锚点/子格。
// 传入的是"想要走多少"（已含速度、坡度、对角归一化），返回是否发生了跨格。
//
// 两层顺序是有意的：形状层先决定"实际能走到哪"，格子层再校验水/悬崖（地形），
// 不可走就贴边停在边界外侧——占位物从不参与这一层判定，它们只在形状层拦人。
// dir 是本次的意图方向，用于跨格后弹掉队首路径点。
func MoveBody(
	w *ecs.World, p *components.Position, mv *components.Moveable,
	dir components.MoveDir, stepX, stepY float64,
) bool {
	if stepX == 0 && stepY == 0 {
		return false
	}
	if dir.DX != 0 || dir.DY != 0 {
		// 胶囊轴向跟随最近一次移动意图（静止时保留，避免身体突然转 90°）
		mv.FacingX, mv.FacingY = dir.DX, dir.DY
	}
	wx := float64(p.X) + mv.SubX
	wy := float64(p.Y) + mv.SubY
	stepX, stepY, _ = SlideStep(w, BodyOf(wx, wy, mv), stepX, stepY)

	moved := false
	// 每轴独立推进：sub 是 [0,1) 分数偏移，渲染位置 = Position + sub。
	// 正方向 sub 递增、满 1 跨格；负方向 sub 递减、过 0 跨格（借位回 [0,1)）。
	// 跨格时校验目标格可走，不可走按方向钳位在边界外侧，客户端同公式同步停。
	if stepX != 0 {
		var crossed bool
		p.X, mv.SubX, crossed = stepAxis(p.X, mv.SubX, stepSign(stepX), math.Abs(stepX), func(x int) bool {
			return walkable(w, x, int(p.Y))
		})
		if crossed {
			moved = true
			popPathStep(mv, dir)
		}
	}
	if stepY != 0 {
		var crossed bool
		p.Y, mv.SubY, crossed = stepAxis(p.Y, mv.SubY, stepSign(stepY), math.Abs(stepY), func(y int) bool {
			return walkable(w, int(p.X), y)
		})
		if crossed {
			moved = true
			popPathStep(mv, dir)
		}
	}
	return moved
}

// stepSign 位移分量的方向（-1/1）。
func stepSign(v float64) int {
	if v < 0 {
		return -1
	}
	return 1
}

// BodyOf 由移动体状态拼出碰撞形状（圆柱或胶囊），位置取 (x, y)（格，浮点）。
// 实体没带半径（BodyRadius = 0）时用缺省 BodyRadius。
func BodyOf(x, y float64, mv *components.Moveable) collision.Body {
	radius := mv.BodyRadius
	if radius <= 0 {
		radius = BodyRadius
	}
	return collision.Body{
		X:          x,
		Z:          y,
		Radius:     radius,
		HalfLength: mv.BodyHalfLength,
		FaceX:      float64(mv.FacingX),
		FaceZ:      float64(mv.FacingY),
	}
}
