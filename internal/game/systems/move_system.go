package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/effect"
	"starve/internal/game/worldmap"
)

// MoveSystem 移动推进（order 95）：效果（90）之后、生存（100）之前。
// tick 制移动：move 命令是方向步进，进 Moveable.Queue 缓存（顺序应用，
// 0,0 停止命令清空队列）；本系统每 tick 累计 Elapsed，到步进间隔就消费队首一步。
// 步进间隔按速度修正百分比调整（+100% = 间隔减半，最小 1 tick）；
// 修正 ≤ -100% 视为完全冻结，不移动。
type MoveSystem struct{}

// Update 实现 ECS 系统接口。
func (s *MoveSystem) Update(w *ecs.World, dt time.Duration) {
	ecs.Query2[components.Moveable, components.Position](w, func(e ecs.Entity, mv *components.Moveable, p *components.Position) {
		if len(mv.Queue) == 0 {
			return
		}
		interval := mv.Interval
		if interval <= 0 {
			interval = 2 // 兜底：与默认配置一致（20Hz 下 10 格/秒）
		}
		if mod := effect.SpeedModPercent(w, e); mod != 0 {
			if 100+mod <= 0 {
				return // 速度被完全冻结
			}
			interval = interval * 100 / (100 + mod)
			if interval < 1 {
				interval = 1
			}
		}
		mv.Elapsed++
		moved := false
		for mv.Elapsed >= interval && len(mv.Queue) > 0 {
			mv.Elapsed -= interval
			d := mv.Queue[0]
			mv.Queue = mv.Queue[1:]
			if md, ok := ecs.TryResource[worldmap.MapData](w); ok {
				if !md.Walkable(p.X+d.DX, p.Y+d.DY) {
					continue // 撞墙/不可走：丢弃这一步（建筑阻挡）
				}
			}
			p.X += d.DX
			p.Y += d.DY
			moved = true
		}
		if moved {
			ecs.MarkDirty[components.Position](w, e)
		}
		ecs.MarkDirty[components.Moveable](w, e) // Elapsed/Queue 变化（存档/快照）
	})
}
