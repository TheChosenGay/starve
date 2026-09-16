package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/config"
	"starve/internal/game/worldmap"
)

// seedResources 按配置创建资源/环境实体（按配置顺序，确定性）。
// 按动作挂受激能力组件（Choppable/Minable/Pickable，-able）；
// 无动作的环境物只保留 Position + Scenery 身份。
// 占位分两种形状（见 components.Block 注释，两者都是"占位 ≠ 不可走"）：
//   - collision_radius > 0（树/岩）：格心圆柱，占 1 格，寻路代价低（可穿过但要绕更好）；
//   - blocking（整格障碍物）：占格盒，寻路代价高（角色挤不进去）。
func seedResources(sim *ecs.World, seeds []worldmap.SeededResource, templates map[components.ItemKind]ItemTemplate) {
	for _, s := range seeds {
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: s.X, Y: s.Y})
		if s.Action == 0 {
			ecs.Add(sim, e, components.Scenery{Kind: s.Kind})
		} else {
			ecs.Add(sim, e, components.DropSource{Category: components.DropSourceResource, ResourceKind: s.Kind})
		}
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
		// 环境物是静态实体；形状（Collide）与占位（Block）分开挂：
		//   - collision_radius > 0：格心圆，占 1 格（树/岩）；
		//   - blocking：整格盒（不可穿的环境物）。
		switch tpl := templates[s.Kind]; {
		case tpl.CollisionRadius > 0:
			ecs.Add(sim, e, components.Collide{
				Shape:  components.CollideShapeCircle,
				Radius: tpl.CollisionRadius,
			})
			ecs.Add(sim, e, components.Block{Width: 1, Height: 1, Thin: true})
		case tpl.Blocking:
			ecs.Add(sim, e, components.Collide{
				Shape:  components.CollideShapeBox,
				Width:  1,
				Height: 1,
			})
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
		ecs.Add(sim, e, components.Collide{Shape: components.CollideShapeBox, Width: 1, Height: 1})
	}
}

// seedRevivalStatues 创建独立复活雕像实体，不复用 Workstation 语义。
func seedRevivalStatues(sim *ecs.World, statues []worldmap.RevivalStatueSeed) {
	for _, statue := range statues {
		uses := statue.Uses
		if uses <= 0 {
			uses = components.DefaultHauntUses
		}
		duration := statue.DurationTicks
		if duration <= 0 {
			duration = components.DefaultHauntDuration
		}
		e := sim.CreateEntity()
		ecs.Add(sim, e, components.Position{X: statue.X, Y: statue.Y})
		ecs.Add(sim, e, components.Hauntable{
			RemainingUses: uses,
			DurationTicks: duration,
		})
		ecs.Add(sim, e, components.Block{Width: 1, Height: 1})
		ecs.Add(sim, e, components.Collide{Shape: components.CollideShapeBox, Width: 1, Height: 1})
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
		ecs.Add(sim, e, components.Moveable{
			Speed: intervalToSpeed(tpl.MoveInterval, tickSec),
		})
		// 生物是动态实体；碰撞体单独挂 Collide（由客户端模型推导，见 docs/模型到碰撞体流水线.md）
		ecs.Add(sim, e, components.Collide{
			Shape:      components.CollideShapeCapsule,
			Radius:     tpl.BodyRadius,
			HalfLength: tpl.BodyHalfLength,
			BodyHeight: tpl.BodyHeight,
		})
		// AOI 半径取"感知半径"与"仇恨传播半径"的**较大者**。
		//
		// 为什么不能各自一个 AOI：AOI.Visible 是由 AOISystem 每轮**整体重建**的
		// 感知缓存（见 aoi_system.go 第一轮 `aoi.Visible = aoi.Visible[:0]`），
		// 一个实体只有一份 Visible。所以要让"仇恨传播能覆盖更大范围"，
		// 就必须把 AOI 半径本身放大到仇恨半径。
		//
		// 副作用：感知范围跟着变大（狼更早发现玩家）。对掠食者这是可接受的
		// ——它本来就在"闻着血腥味追"；若要让两者真正独立，需要给 AOI 增加
		// 分层（例如 Visible 之外再来一个 ThreatVisible），那是更大的改动。
		aoiRadius := tpl.PerceptionRadius
		if tpl.ThreatRadius > aoiRadius {
			aoiRadius = tpl.ThreatRadius
		}
		ecs.Add(sim, e, components.AOI{Radius: aoiRadius})
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
			Leash:          tpl.Leash,
		})
		ecs.Add(sim, e, interactive.Attacker{
			AttackDamage:   tpl.AttackDamage,
			AttackRange:    tpl.AttackRange,
			AttackCooldown: tpl.AttackCooldown,
		})
		// 行为树：配置显式指定则用配置，否则按"能否攻击"推断
		// （能攻击 = 掠食者树，否则被动树）。决策逻辑从此由树表达。
		treeKind := tpl.TreeKind
		if treeKind == components.TreeKindUnspecified {
			treeKind = components.TreeKindForTemplate(tpl.AttackDamage > 0)
		}
		ecs.Add(sim, e, components.BehaviorTree{
			Kind:         treeKind,
			RunningChild: map[uint32]uint8{},
			Counters:     map[uint32]int{},
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
