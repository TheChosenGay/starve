package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// pairEntry 一对自动行为匹配：activer ↔ actived ↔ behavior。
// 注册行本身就是映射（泛型在编译期绑定 A↔R，等价 activer→actived / activer→behavior 两张表），
// 具体能力（是否有 -er / 作用距离 / 目标可用性 / 就近查找）由泛型注册生成闭包，FindBest 等运行时遍历。
type pairEntry struct {
	intent   interactive.Intent
	behavior interactive.Behavior
	active   func(w *ecs.World, actor ecs.Entity) bool                   // 作用者是否有该 -er（含装备解析）
	actRange func(w *ecs.World, actor ecs.Entity) int                    // 该 -er 当前作用距离
	usable   func(w *ecs.World, e ecs.Entity) bool                       // 目标是否仍可被作用（组件自持）
	nearest  func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity // radius 内最近的可用目标
}

var pairs []*pairEntry
var byIntent = map[interactive.Intent]*pairEntry{}

// RegisterPair 注册一对自动行为候选：
// A 实现 Activer（-er），R 实现 Actived（-able），b 为对应行为。
// 作用距离取 A.ActRange()；目标要求 R.Usable 通过（未耗尽/存活，组件自持判断）。
func RegisterPair[A interactive.Activer, R interactive.Actived](intent interactive.Intent, b interactive.Behavior) {
	p := &pairEntry{intent: intent, behavior: b}
	p.active = func(w *ecs.World, actor ecs.Entity) bool {
		_, c := interactive.ActorCap[A](w, actor)
		return c != nil
	}
	p.actRange = func(w *ecs.World, actor ecs.Entity) int {
		if _, c := interactive.ActorCap[A](w, actor); c != nil {
			return (*c).ActRange()
		}
		return 0
	}
	p.usable = func(w *ecs.World, e ecs.Entity) bool {
		return ecs.Has[R](w, e) && (*ecs.Get[R](w, e)).Usable(w, e)
	}
	p.nearest = func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity {
		return nearestWith[R](w, actor, radius, func(e ecs.Entity) bool {
			return (*ecs.Get[R](w, e)).Usable(w, e)
		})
	}
	pairs = append(pairs, p)
	byIntent[intent] = p
}

// FindBest 在 radius（AOI 感知半径）内找最近的可执行行为：
// 每个能力的搜索半径 = min(AOI, 作用距离)（交互不能超出感知），
// 只接受行为真正可执行的候选，全部候选中取距离最近的一对；
// 距离相同按注册顺序（chop → mine → pick → attack）稳定取胜。
// 返回（意图, 目标实体, 是否找到）。
func FindBest(w *ecs.World, actor ecs.Entity, radius int) (interactive.Intent, ecs.Entity, bool) {
	if radius <= 0 {
		radius = 2
	}
	return pickNearest(w, actor, radius, func(p *pairEntry) int {
		er := p.actRange(w, actor)
		if er <= 0 {
			er = 2
		}
		if er > radius {
			er = radius
		}
		return er
	})
}

// FindWalkTarget 兜底：在 radius（整个 AOI）内找最近的匹配目标，
// 不限制在 -er 作用距离内——用于"超出交互距离，走过去"。
// 返回（意图, 目标实体, 是否找到）。
func FindWalkTarget(w *ecs.World, actor ecs.Entity, radius int) (interactive.Intent, ecs.Entity, bool) {
	if radius <= 0 {
		radius = 2
	}
	return pickNearest(w, actor, radius, func(p *pairEntry) int { return radius })
}

// pickNearest 按 search 给出的每个能力的搜索半径取候选，全部候选中取最近的一对。
func pickNearest(w *ecs.World, actor ecs.Entity, radius int, search func(p *pairEntry) int) (interactive.Intent, ecs.Entity, bool) {
	var bestIntent interactive.Intent
	var bestTarget ecs.Entity
	bestDist := radius + 1
	for _, p := range pairs {
		if !p.active(w, actor) {
			continue
		}
		t := p.nearest(w, actor, search(p))
		if t == 0 {
			continue
		}
		if d := manhattanBetween(w, actor, t); d < bestDist {
			bestDist, bestIntent, bestTarget = d, p.intent, t
		}
	}
	return bestIntent, bestTarget, bestTarget != 0
}

// HasCapability 作用者是否仍拥有该意图对应的 -er 能力（含装备解析）。
func HasCapability(w *ecs.World, actor ecs.Entity, intent interactive.Intent) bool {
	if p, ok := byIntent[intent]; ok {
		return p.active(w, actor)
	}
	return false
}

// Usable 目标对该意图是否仍可被作用（未耗尽/存活等）。
func Usable(w *ecs.World, target ecs.Entity, intent interactive.Intent) bool {
	if p, ok := byIntent[intent]; ok {
		return p.usable(w, target)
	}
	return false
}

// InRange 目标是否已在作用距离内（距离 ≤ -er 当前作用距离）。
func InRange(w *ecs.World, actor, target ecs.Entity, intent interactive.Intent) bool {
	p, ok := byIntent[intent]
	if !ok {
		return false
	}
	er := p.actRange(w, actor)
	if er <= 0 {
		er = 2
	}
	return manhattanBetween(w, actor, target) <= er
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
