package systems

import (
	"math"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/effect"
	"starve/internal/game/worldmap"
)

// MoveSystem 移动推进（order 95）：效果（90）之后、生存（100）之前。
// 连续速度模型：每 tick 按 speed×dt 沿有效方向（Path 队首或输入方向）累积子格偏移，
// 跨格时提交到 Position（整格），目标格不可走则贴墙停在边界；
// 速度修正百分比作用于 speed（+100% = 翻倍；≤ -100% = 完全冻结）。
type MoveSystem struct{}

// Update 实现 ECS 系统接口。
func (s *MoveSystem) Update(w *ecs.World, dt time.Duration) {
	dtSec := dt.Seconds()
	ecs.Query2[components.Moveable, components.Position](w, func(e ecs.Entity, mv *components.Moveable, p *components.Position) {
		spd := effectiveSpeed(w, e, mv.Speed)
		if mv.EffectiveSpeed != spd {
			mv.EffectiveSpeed = spd
			ecs.MarkDirty[components.Moveable](w, e)
		}
		dir := effectiveDir(mv)
		if dir.DX == 0 && dir.DY == 0 {
			// 连续移动在任意子格位置都可停止；保留 sub，避免松键后吸附整数格。
			return
		}
		if spd <= 0 {
			return // 速度效果完全冻结
		}
		moved := false
		// 每轴独立推进：sub 是 [0,1) 分数偏移，渲染位置 = Position + sub。
		// 正方向 sub 递增、满 1 跨格；负方向 sub 递减、过 0 跨格（借位回 [0,1)）。
		// 跨格时校验目标格可走，不可走按方向钳位在边界外侧，客户端同公式同步停。
		dist := spd * dtSec
		if dir.DX != 0 && dir.DY != 0 {
			// 对角归一化：任意方向同速（每轴分量 ÷√2）
			k := 1 / math.Sqrt2
			dist *= k
		}
		if dir.DX != 0 {
			var ok bool
			p.X, mv.SubX, ok = stepAxis(p.X, mv.SubX, dir.DX, dist, func(x int) bool {
				return walkable(w, x, int(p.Y))
			})
			if ok {
				moved = true
				popPathStep(mv, dir)
			}
		}
		if dir.DY != 0 {
			var ok bool
			p.Y, mv.SubY, ok = stepAxis(p.Y, mv.SubY, dir.DY, dist, func(y int) bool {
				return walkable(w, int(p.X), y)
			})
			if ok {
				moved = true
				popPathStep(mv, dir)
			}
		}
		if moved {
			ecs.MarkDirty[components.Position](w, e)
		}
		ecs.MarkDirty[components.Moveable](w, e) // Dir/Sub/Path 变化（存档/快照）
	})
}

func effectiveSpeed(w *ecs.World, e ecs.Entity, base float64) float64 {
	if base <= 0 {
		base = 10
	}
	mod := effect.SpeedModPercent(w, e)
	if 100+mod <= 0 {
		return 0
	}
	return base * float64(100+mod) / 100
}

// stepAxis 推进一轴：返回 (新锚点格, 新 sub, 是否跨格)。
// 跨格时目标格不可走则停在边界（正方向 0.999 / 负方向 0.001，即边界外侧 ε）。
func stepAxis(pos int, sub float64, dir int, dist float64, canWalk func(int) bool) (int, float64, bool) {
	// 贴墙保持：已在边界且目标格不可走 → 不再累积（否则 sub 会在 0.001↔0.5 间震荡，贴墙抖动）
	if (sub <= 0.002 && dir < 0 && !canWalk(pos-1)) ||
		(sub >= 0.998 && dir > 0 && !canWalk(pos+1)) {
		return pos, sub, false
	}
	next := sub + float64(dir)*dist
	if next >= 0 && next < 1 {
		return pos, next, false // 未跨格：只更新分数偏移
	}
	npos := pos + dir
	if !canWalk(int(npos)) {
		if dir > 0 {
			return pos, 0.999, false
		}
		return pos, 0.001, false
	}
	if next < 0 {
		return npos, next + 1, true // 负方向跨格：借位回到 [0,1)
	}
	return npos, next - 1, true
}

// effectiveDir 当前移动方向：路径优先（自动行走/AI 追击），否则用输入方向。
func effectiveDir(mv *components.Moveable) components.MoveDir {
	if len(mv.Path) > 0 {
		return mv.Path[0]
	}
	return components.MoveDir{DX: mv.DirX, DY: mv.DirY}
}

// popPathStep 跨格完成一步路径后弹出队首（方向一致时）。
func popPathStep(mv *components.Moveable, dir components.MoveDir) {
	if len(mv.Path) > 0 && mv.Path[0] == dir {
		mv.Path = mv.Path[1:]
	}
}

// walkable 目标格可走（无地图 = 可走）。
func walkable(w *ecs.World, x, y int) bool {
	if md, ok := ecs.TryResource[worldmap.MapData](w); ok {
		return md.Walkable(x, y)
	}
	return true
}
