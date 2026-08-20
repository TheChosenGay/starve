package effect

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// drainHealth 扣血型效果的通用结算：param = 每 tick 扣血量（0 = 默认 1）。
func drainHealth(w *ecs.World, e ecs.Entity, param int, cause game.HealthChangeCause) {
	if param <= 0 {
		param = 1
	}
	components.ApplyHealthDelta(w, e, 0, -param, cause, 0)
}
