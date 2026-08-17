package behavior

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// CapabilityPair 上下文行为匹配：-er 主动能力 ↔ -able 受激能力 → 行为意图。
// 空格键等"自动行为"用它按距离找最近的可执行目标。
type CapabilityPair struct {
	Intent   interactive.Intent
	Active   ecs.ComponentID // -er 组件名（说明用）
	Reactive ecs.ComponentID // -able 组件名（说明用）
	// HasActive 作用者是否拥有该 -er（含装备解析）。
	HasActive func(w *ecs.World, actor ecs.Entity) bool
	// Range 该 -er 的作用距离（数据来自能力组件；FindBest 用它限定搜索半径）。
	Range func(w *ecs.World, actor ecs.Entity) int
	// Nearest 返回 radius 内最近的、带匹配 -able 的实体。
	Nearest func(w *ecs.World, actor ecs.Entity, radius int) ecs.Entity
}

var pairs []CapabilityPair

// RegisterPair 注册一对 -er ↔ -able ↔ Behavior 的匹配（自动行为候选）。
func RegisterPair(p CapabilityPair) { pairs = append(pairs, p) }

// FindBest 在 radius（AOI 感知半径）内找最近的可执行行为：
// 遍历作用者的 -er，每个能力的搜索半径 = min(AOI, -er.Range)（交互不能超出感知），
// 只接受行为真正可执行的候选（未耗尽/存活），全部候选中取距离最近的一对；
// 距离相同按注册顺序（chop → mine → pick → attack）稳定取胜。
// 返回（意图, 目标实体, 是否找到）。
func FindBest(w *ecs.World, actor ecs.Entity, radius int) (interactive.Intent, ecs.Entity, bool) {
	if radius <= 0 {
		radius = 2
	}
	var bestIntent interactive.Intent
	var bestTarget ecs.Entity
	bestDist := radius + 1
	for _, p := range pairs {
		if !p.HasActive(w, actor) {
			continue
		}
		er := p.Range(w, actor)
		if er <= 0 {
			er = 2
		}
		if er > radius {
			er = radius
		}
		t := p.Nearest(w, actor, er)
		if t == 0 {
			continue
		}
		if d := manhattanBetween(w, actor, t); d < bestDist {
			bestDist, bestIntent, bestTarget = d, p.Intent, t
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
