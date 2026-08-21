package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/config"
	"starve/internal/game/worldmap"
)

// seedResources 按配置创建可采集实体（按配置顺序，确定性）。
// 按动作挂受激能力组件（Choppable/Minable/Pickable，-able）；
// 模板标记 blocking 的环境物（树/岩）额外挂 Block，占格阻挡移动/寻路。
func seedResources(sim *ecs.World, seeds []worldmap.SeededResource, templates map[components.ItemKind]ItemTemplate) {
	for _, s := range seeds {
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		ecs.Add(sim, e, components.DropSource{Category: components.DropSourceResource, ResourceKind: s.Kind})
		switch s.Action {
		case components.WorkChop:
			ecs.Add(sim, e, interactive.Choppable{Kind: s.Kind, WorkLeft: s.Work, MaxWork: s.Work})
		case components.WorkMine:
			ecs.Add(sim, e, interactive.Minable{Kind: s.Kind, WorkLeft: s.Work, MaxWork: s.Work})
		case components.WorkPick:
			ecs.Add(sim, e, interactive.Pickable{Kind: s.Kind, WorkLeft: s.Work, MaxWork: s.Work})
		}
		if tpl, ok := templates[s.Kind]; ok && tpl.RespawnTicks > 0 {
			// 可重生：耗尽后到点恢复工作量（与工作类型解耦，只看 Respawnable）
			ecs.Add(sim, e, components.Respawnable{Ticks: tpl.RespawnTicks})
		}
		if templates[s.Kind].Blocking {
			ecs.Add(sim, e, components.Block{Width: 1, Height: 1})
		}
	}
}

// seedStations 按配置创建工作站实体；实体工作站占据一格，参与移动与寻路阻挡。
func seedStations(sim *ecs.World, stations []worldmap.StationSeed) {
	for _, s := range stations {
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		ecs.Add(sim, e, components.Workstation{Type: components.WorkstationTypeByName[s.Type]})
		ecs.Add(sim, e, components.Block{Width: 1, Height: 1})
	}
}

// seedLoot 生成初始可拾取物资实体（Loot）。
func seedLoot(sim *ecs.World, loots []worldmap.LootSeed) {
	for _, l := range loots {
		k, ok := components.ItemKindByName[l.Kind]
		if !ok || l.Count <= 0 {
			continue
		}
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: l.X, Y: l.Y})
		ecs.Add(sim, e, components.Lootable{Items: []components.ItemStack{{Kind: k, Count: l.Count}}})
	}
}

// seedEmitters 生成手摆效果发射器实体（增益植物/火堆等）。
func seedEmitters(sim *ecs.World, emitters []worldmap.EmitterSeed) {
	for _, s := range emitters {
		if len(s.Effects) == 0 || s.Radius < 0 {
			continue
		}
		var instances []components.EffectInstance
		for _, ins := range s.Effects {
			if o, ok := components.EffectOrderByName[ins.Order]; ok {
				instances = append(instances, components.EffectInstance{Order: o, Param: ins.Param})
			}
		}
		if len(instances) == 0 {
			continue
		}
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		ecs.Add(sim, e, components.EffectEmitter{Effects: instances, Radius: s.Radius})
	}
}

// seedCreatures 按配置创建生物实体（Position + Health + Creature + Moveable）。
// 模板静态属性在生成时拷贝进组件（快照/存档自包含）。
// seedCreatures 按配置创建生物实体；Moveable 用连续速度（模板 MoveInterval 转格/秒，tickSec 为单 tick 秒数）。
func seedCreatures(sim *ecs.World, seeds []worldmap.CreatureSeed, templates map[components.CreatureKind]config.CreatureTemplate, tickSec float64) {
	for _, s := range seeds {
		kind, ok := components.CreatureKindByName[s.Kind]
		if !ok {
			continue
		}
		tpl, ok := templates[kind]
		if !ok {
			continue
		}
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		ecs.Add(sim, e, components.Health{Cur: tpl.HP, Max: tpl.HP})
		ecs.Add(sim, e, components.Attackable{})
		ecs.Add(sim, e, components.Moveable{Speed: intervalToSpeed(tpl.MoveInterval, tickSec)})
		ecs.Add(sim, e, components.AOI{Radius: tpl.PerceptionRadius})
		ecs.Add(sim, e, components.Creature{
			Kind:       kind,
			Threats:    map[ecs.Entity]int32{},
			HomeX:      s.X,
			HomeY:      s.Y,
			RoamRadius: tpl.RoamRadius,
		})
		ecs.Add(sim, e, components.DropSource{Category: components.DropSourceCreature, CreatureKind: kind})
		ecs.Add(sim, e, components.AI{
			State:          components.CreatureIdle,
			FleeHP:         int(float32(tpl.HP) * tpl.FleeHPRatio),
			HitMemoryTicks: tpl.HitMemoryTicks,
			HostileKinds:   tpl.HostileKinds,
			HostilePlayers: tpl.HostilePlayers,
		})
		ecs.Add(sim, e, interactive.Attacker{
			AttackDamage:   tpl.AttackDamage,
			AttackRange:    tpl.AttackRange,
			AttackCooldown: tpl.AttackCooldown,
		})
	}
}

// intervalToSpeed 把"每 interval tick 走一格"换算为连续速度（格/秒）。
func intervalToSpeed(interval int, tickSec float64) float64 {
	if interval <= 0 || tickSec <= 0 {
		return 10
	}
	return 1 / (float64(interval) * tickSec)
}
