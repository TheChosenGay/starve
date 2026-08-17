package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// withinRange 校验 actor 到 target 的距离 ≤ rng（缺省 2）。
func withinRange(w *ecs.World, actor, target ecs.Entity, rng int) bool {
	if rng <= 0 {
		rng = 2
	}
	if !ecs.Has[components.Position](w, actor) || !ecs.Has[components.Position](w, target) {
		return false
	}
	return ecs.Get[components.Position](w, actor).WithinRange(*ecs.Get[components.Position](w, target), rng)
}

// applyDepletion 目标耗尽后的世界层状态：
// 可重生（Respawnable，创建时已挂载）由 RespawnSystem 自动倒计时恢复，这里不手动挂；
// 否则按需挂 Dead。
func applyDepletion(w *ecs.World, target ecs.Entity, deadOnNoRespawn bool) {
	if ecs.Has[components.Respawnable](w, target) {
		return
	}
	if deadOnNoRespawn {
		ecs.Add(w, target, components.Dead{Reason: "worked"})
	}
}
