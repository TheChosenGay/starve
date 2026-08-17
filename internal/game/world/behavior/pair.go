package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// pairEntry 一对自动行为匹配：activer ↔ actived ↔ behavior。
// 注册行本身就是映射（泛型在编译期绑定 A↔R，等价 activer→actived / activer→behavior 两张表），
// FindBest 运行时只遍历注册表，不需要反射。
type pairEntry struct {
	intent   interactive.Intent
	behavior interactive.Behavior
	// find 返回 radius 内最近的、可被该行为作用的目标（0 = 无）。
	find func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity
}

var pairs []pairEntry

// RegisterPair 注册一对自动行为候选：
// A 实现 Activer（-er），R 实现 Actived（-able），b 为对应行为。
// 作用距离取 A.ActRange()，搜索半径 = min(AOI, 作用距离)（交互不能超出感知）；
// 目标要求 R.Usable 通过（未耗尽/存活，组件自持判断）。
func RegisterPair[A interactive.Activer, R interactive.Actived](intent interactive.Intent, b interactive.Behavior) {
	pairs = append(pairs, pairEntry{
		intent:   intent,
		behavior: b,
		find: func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity {
			_, c := interactive.ActorCap[A](w, actor)
			if c == nil {
				return 0 // 作用者没有该 -er（含装备解析）
			}
			er := (*c).ActRange()
			if er <= 0 {
				er = 2
			}
			if er > radius {
				er = radius
			}
			return nearestWith[R](w, actor, er, func(e ecs.Entity) bool {
				return (*ecs.Get[R](w, e)).Usable(w, e)
			})
		},
	})
}

// FindBest 在 radius（AOI 感知半径）内找最近的可执行行为：
// 遍历注册的 activer→actived 对，只接受行为真正可执行的候选（R.Usable），
// 全部候选中取距离最近的一对；距离相同按注册顺序（chop → mine → pick → attack）稳定取胜。
// 返回（意图, 目标实体, 是否找到）。
func FindBest(w *ecs.World, actor ecs.Entity, radius int) (interactive.Intent, ecs.Entity, bool) {
	if radius <= 0 {
		radius = 2
	}
	var bestIntent interactive.Intent
	var bestTarget ecs.Entity
	bestDist := radius + 1
	for _, p := range pairs {
		t := p.find(w, actor, radius)
		if t == 0 {
			continue
		}
		if d := manhattanBetween(w, actor, t); d < bestDist {
			bestDist, bestIntent, bestTarget = d, p.intent, t
		}
	}
	return bestIntent, bestTarget, bestTarget != 0
}

// nearestWith 返回 radius 内最近的、带组件 R 且 usable 通过（可执行）的实体（排除 actor 自身）。
// 距离用曼哈顿距离；同距取实体 id 最小（Query 顺序确定性）。
func nearestWith[R any](w *ecs.World, actor ecs.Entity, radius int, usable func(e ecs.Entity) bool) ecs.Entity {
	if !ecs.Has[components.Position](w, actor) {
		return 0
	}
	ap := ecs.Get[components.Position](w, actor)
	var best ecs.Entity
	bestDist := radius + 1
	ecs.Query2[R, components.Position](w, func(e ecs.Entity, _ *R, p *components.Position) {
		if e == actor || !usable(e) {
			return
		}
		dx, dy := ap.X-p.X, ap.Y-p.Y
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if d := dx + dy; d <= radius && (d < bestDist || (d == bestDist && e < best)) {
			bestDist, best = d, e
		}
	})
	return best
}

// manhattanBetween 两实体的曼哈顿距离（缺位置 = 极大值）。
func manhattanBetween(w *ecs.World, a, b ecs.Entity) int {
	if !ecs.Has[components.Position](w, a) || !ecs.Has[components.Position](w, b) {
		return 1 << 30
	}
	pa := ecs.Get[components.Position](w, a)
	pb := ecs.Get[components.Position](w, b)
	dx, dy := pa.X-pb.X, pa.Y-pb.Y
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}
