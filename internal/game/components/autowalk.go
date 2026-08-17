package components

import (
	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// AutoWalk 空格兜底自动行走：目标在 AOI 内但超出交互距离时挂载到作用者，
// 世界层每 tick 朝目标走一步，进入交互距离后自动执行一次行为并移除。
// 瞬态组件：不注册 codec（不进快照/存档），手动移动或目标消失会取消。
type AutoWalk struct {
	Target ecs.Entity
	Intent game.WorkAction
}

// RegisterAutoWalk 注册 AutoWalk 组件名（无 codec：瞬态状态不落盘/不下发）。
func RegisterAutoWalk(w *ecs.World) {
	ecs.RegisterComponent[AutoWalk](w, "AutoWalk")
}
