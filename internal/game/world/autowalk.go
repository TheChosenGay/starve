package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/world/behavior"
)

// driveAutoWalks 空格兜底自动行走（每 tick，RunSystems 之后）：
//   - 作用者死亡/失去能力、目标消失/不可用 → 取消 AutoWalk；
//   - 目标已在交互距离内 → 执行一次行为（复用命令层副作用）并移除；
//   - 否则朝目标走一步（MoveSystem 下一 tick 消费）。
func (a *WorldActor) driveAutoWalks() {
	var done []ecs.Entity
	ecs.Query[components.AutoWalk](a.sim, func(e ecs.Entity, aw *components.AutoWalk) {
		if !a.sim.IsAlive(e) || ecs.Has[components.Dead](a.sim, e) ||
			!a.sim.IsAlive(aw.Target) || !behavior.Usable(a.sim, aw.Target, aw.Intent) ||
			!behavior.HasCapability(a.sim, e, aw.Intent) {
			done = append(done, e)
			return
		}
		if behavior.InRange(a.sim, e, aw.Target, aw.Intent) {
			if uid, ok := a.players[e]; ok {
				if aw.Intent == interactive.IntentAttack {
					interactive.Do(a.sim, e, aw.Target, aw.Intent)
				} else {
					a.cmds.work(uid, e, aw.Target, aw.Intent) // PICK 入包 / 工具损坏卸下
				}
			}
			done = append(done, e)
			return
		}
		a.stepToward(e, aw.Target)
	})
	for _, e := range done {
		ecs.Remove[components.AutoWalk](a.sim, e)
	}
}

// stepToward 朝目标走一步：优先 x 轴、其次 y 轴；目标格不可走则换轴，
// 两轴都不可走则本 tick 不动（下 tick 重试，墙角等场景由手动接管）。
func (a *WorldActor) stepToward(e, target ecs.Entity) {
	if !ecs.Has[components.Position](a.sim, e) || !ecs.Has[components.Position](a.sim, target) {
		return
	}
	mv := ecs.Ensure[components.Moveable](a.sim, e)
	if len(mv.Queue) >= maxMoveQueue {
		return
	}
	pe := ecs.Get[components.Position](a.sim, e)
	pt := ecs.Get[components.Position](a.sim, target)
	dx, dy := clampDir(pt.X-pe.X), clampDir(pt.Y-pe.Y)
	for _, dir := range [2]components.MoveDir{{DX: dx, DY: 0}, {DX: 0, DY: dy}} {
		if dir.DX == 0 && dir.DY == 0 {
			continue
		}
		if !a.walkable(pe.X+dir.DX, pe.Y+dir.DY) {
			continue
		}
		mv.Queue = append(mv.Queue, dir)
		ecs.MarkDirty[components.Moveable](a.sim, e)
		return
	}
}

// walkable 目标格是否可走（无地图 = 可走）。
func (a *WorldActor) walkable(x, y int) bool {
	if md, ok := ecs.TryResource[MapData](a.sim); ok {
		return md.Walkable(x, y)
	}
	return true
}
