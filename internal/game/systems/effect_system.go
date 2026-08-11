package systems

import (
	"sort"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/effect"
)

// EffectSystem 效果结算（order 90，先于 MoveSystem/生存系统）：
// 每 tick 对带 Effects 组件的实体（玩家）计算"当前覆盖集" =
//
//	脚下地块效果（MapData 资源）+ 半径内 EffectEmitter 实体效果；
//
// 与 Active 求集合差 → 新覆盖 OnEnter / 持续 OnTick / 离开 OnExit → 更新状态。
// 每个覆盖源携带 param（效果强度），多源求和后传给效果实现
// （如两个毒源 param 1+2 → 每 tick 扣 3 血；加速/减速 = 同一个 SPEED 效果，
// param 正负决定方向）。
// 速度修正等由效果产生，本系统跑完后 MoveSystem 即可消费（同 tick 生效）。
// 确定性：实体按 ID 升序、效果按 EffectOrder 升序遍历。
type EffectSystem struct{}

// effectEmitter 一枚发射器实体（位置 + 效果集合 + 半径）。
type effectEmitter struct {
	e ecs.Entity
	c *components.EffectEmitter
	p *components.Position
}

// Update 实现 ECS 系统接口（在 RunSystems 内按 order 调用）。
func (s *EffectSystem) Update(w *ecs.World, dt time.Duration) {
	// 发射器（实体 ID 升序）
	var emitters []effectEmitter
	ecs.Query2[components.EffectEmitter, components.Position](w, func(e ecs.Entity, ec *components.EffectEmitter, p *components.Position) {
		if w.IsAlive(e) && !ecs.Has[components.Dead](w, e) {
			emitters = append(emitters, effectEmitter{e, ec, p})
		}
	})
	sort.Slice(emitters, func(i, j int) bool { return emitters[i].e < emitters[j].e })

	// 受击实体（带 Effects 组件的都是效果受体；目前 = 玩家）
	var recipients []ecs.Entity
	ecs.Query[components.Effects](w, func(e ecs.Entity, _ *components.Effects) {
		recipients = append(recipients, e)
	})
	sort.Slice(recipients, func(i, j int) bool { return recipients[i] < recipients[j] })

	for _, e := range recipients {
		if !w.IsAlive(e) || ecs.Has[components.Dead](w, e) {
			continue
		}
		applyCoverage(w, e, emitters)
	}
}

// applyCoverage 计算单个实体的覆盖集（计数 + 聚合参数）并与 Active 求差。
func applyCoverage(w *ecs.World, e ecs.Entity, emitters []effectEmitter) {
	coverage := make(map[components.EffectOrder]int32)
	params := make(map[components.EffectOrder]int32)
	if ecs.Has[components.Position](w, e) {
		p := ecs.Get[components.Position](w, e)
		if md, ok := ecs.TryResource[components.MapData](w); ok {
			if o, prm := md.TileEffectAt(p.X, p.Y); o != 0 {
				coverage[o]++
				params[o] += int32(prm)
			}
		}
		for _, em := range emitters {
			if em.c.Radius <= 0 {
				if em.p.X != p.X || em.p.Y != p.Y {
					continue // 半径 0：仅自身所在格
				}
			} else if !p.WithinRange(*em.p, em.c.Radius) {
				continue
			}
			for _, ins := range em.c.Effects {
				if ins.Order == 0 {
					continue
				}
				coverage[ins.Order]++
				params[ins.Order] += int32(ins.Param)
			}
		}
	}

	eff := ecs.Get[components.Effects](w, e)
	if eff.Active == nil {
		eff.Active = map[components.EffectOrder]components.EffectState{}
	}
	for _, o := range unionEffectOrders(coverage, eff.Active) {
		cov := coverage[o]
		prev := eff.Active[o]
		ef := effect.EffectFor(o)
		if ef == nil {
			continue
		}
		if prev.Count == 0 && cov > 0 {
			ef.OnEnter(w, e, int(params[o]))
		}
		if cov > 0 {
			ef.OnTick(w, e, int(params[o]))
		}
		if prev.Count > 0 && cov == 0 {
			ef.OnExit(w, e, 0)
		}
	}

	// 覆盖集变化才 MarkDirty（避免每 tick 全量广播 Effects 组件）
	changed := len(coverage) != len(eff.Active)
	if !changed {
		for o, n := range coverage {
			prev := eff.Active[o]
			if prev.Count != n || prev.Param != params[o] {
				changed = true
				break
			}
		}
	}
	next := make(map[components.EffectOrder]components.EffectState, len(coverage))
	for o, n := range coverage {
		next[o] = components.EffectState{Count: n, Param: params[o]}
	}
	eff.Active = next
	if changed {
		ecs.MarkDirty[components.Effects](w, e)
	}
}

// unionEffectOrders 返回两集合的键并集，按 EffectOrder 升序（确定性）。
func unionEffectOrders[V1, V2 any](a map[components.EffectOrder]V1, b map[components.EffectOrder]V2) []components.EffectOrder {
	seen := make(map[components.EffectOrder]bool, len(a)+len(b))
	out := make([]components.EffectOrder, 0, len(a)+len(b))
	for o := range a {
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	for o := range b {
		if !seen[o] {
			seen[o] = true
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
