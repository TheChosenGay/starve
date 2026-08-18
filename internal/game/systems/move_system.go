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
		dir := effectiveDir(mv)
		if dir.DX == 0 && dir.DY == 0 {
			if mv.SubX != 0 || mv.SubY != 0 {
				// 停下时对齐到整格（服务端发布的 Position 即真实位置，客户端同公式同步）
				mv.SubX, mv.SubY = 0, 0
				ecs.MarkDirty[components.Moveable](w, e)
			}
			return
		}
		spd := mv.Speed
		if spd <= 0 {
			spd = 10 // 兜底：与默认配置一致（10 格/秒）
		}
		if mod := effect.SpeedModPercent(w, e); mod != 0 {
			if 100+mod <= 0 {
				return // 速度被完全冻结
			}
			spd = spd * float64(100+mod) / 100
		}
		vx, vy := float64(dir.DX), float64(dir.DY)
		if vx != 0 && vy != 0 {
			// 对角归一化：任意方向同速（每轴分量 ÷√2）
			k := 1 / math.Sqrt2
			vx, vy = vx*k, vy*k
		}
		mv.SubX += vx * spd * dtSec
		mv.SubY += vy * spd * dtSec
		moved := false
		if mv.SubX >= 1 {
			nx := p.X + dir.DX
			if walkable(w, nx, p.Y) {
				p.X = nx
				mv.SubX -= 1
				moved = true
				popPathStep(mv, dir)
			} else {
				mv.SubX = wallSnap // 贴墙：停在边界（客户端同公式同步停）
			}
		}
		if mv.SubY >= 1 {
			ny := p.Y + dir.DY
			if walkable(w, p.X, ny) {
				p.Y = ny
				mv.SubY -= 1
				moved = true
				popPathStep(mv, dir)
			} else {
				mv.SubY = wallSnap
			}
		}
		if moved {
			ecs.MarkDirty[components.Position](w, e)
		}
		ecs.MarkDirty[components.Moveable](w, e) // Dir/Sub/Path 变化（存档/快照）
	})
}

// wallSnap 贴墙时子格偏移的钳位值（接近 1 但不满格，表示实体停在格边界）。
const wallSnap = 0.999

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
