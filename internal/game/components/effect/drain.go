package effect

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// drainHealth 扣血型效果的通用结算：param = 每 tick 扣血量（0 = 默认 1）。
func drainHealth(w *ecs.World, e ecs.Entity, param int) {
	if !ecs.Has[components.Health](w, e) {
		return
	}
	h := ecs.Get[components.Health](w, e)
	if h.Cur <= 0 {
		return
	}
	if param <= 0 {
		param = 1
	}
	h.Cur -= param
	if h.Cur < 0 {
		h.Cur = 0
	}
	ecs.MarkDirty[components.Health](w, e)
}
