package systems

import (
	"sort"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// AISystem 生物行为状态机（order 92，感知之后、移动之前）：
// 输入 = AOI.Visible（感知）+ LastHitBy（受击窗口）+ HP + 距离；
// 状态转移（每 tick 用当前输入快照评估，不是事件驱动）：
//
//	idle ⇄ chase ⇄ attack；hp ≤ FleeHP → flee；危险解除回 chase/idle。
//
// 输出 = 移动意图（Moveable.Queue）/ 攻击（ApplyAttack 直接结算）。
// 确定性：生物按实体 id 升序；随机游荡用 hash 种子（实体 + 世界时钟）。
type AISystem struct{}

// Update 实现 ECS 系统接口。
func (s *AISystem) Update(w *ecs.World, dt time.Duration) {
	// 生物（升序）
	var creatures []ecs.Entity
	ecs.Query[components.Creature](w, func(e ecs.Entity, _ *components.Creature) {
		creatures = append(creatures, e)
	})
	sort.Slice(creatures, func(i, j int) bool { return creatures[i] < creatures[j] })

	for _, e := range creatures {
		if !w.IsAlive(e) || ecs.Has[components.Dead](w, e) {
			continue
		}
		s.tickAI(w, e)
	}
}

func (s *AISystem) tickAI(w *ecs.World, e ecs.Entity) {
	c := ecs.Get[components.Creature](w, e)
	ai := ecs.Get[components.AI](w, e)
	cp := ecs.Get[components.Position](w, e)
	hp := ecs.Get[components.Health](w, e)
	now := worldPhase(w)
	changed := false

	// 仇恨衰减（每 tick -1，归零移除）
	for t, v := range c.Threats {
		if v <= 1 {
			delete(c.Threats, t)
		} else {
			c.Threats[t] = v - 1
		}
		changed = true
	}
	// 感知：AOI.Visible 里的敌对对象加基础仇恨（候选 = 视野内，不全局扫描）
	if ecs.Has[components.AOI](w, e) {
		for _, v := range ecs.Get[components.AOI](w, e).Visible {
			if !isHostile(w, ai, v) {
				continue
			}
			c.Threats[v]++
			changed = true
		}
	}
	// 受击：窗口内被打 → 给攻击者加高仇恨（"刚被谁打"是强信号）
	if ai.WasHitRecently(now) && ai.LastHitBy != 0 {
		c.Threats[ai.LastHitBy] += 5
		changed = true
	}

	// 目标候选 = AOI.Visible（升序）+ 最近受击者（若在 leash 内）——只处理看得见的，
	// 不遍历全图实体。
	target := ecs.Entity(0)
	best := int32(0)
	leash := 4
	if ecs.Has[components.AOI](w, e) {
		leash += ecs.Get[components.AOI](w, e).Radius
	}
	candidates := append([]ecs.Entity(nil), aoiVisible(w, e)...)
	if ai.WasHitRecently(now) && ai.LastHitBy != 0 {
		candidates = append(candidates, ai.LastHitBy)
	}
	for _, tgt := range candidates {
		t := c.Threats[tgt]
		if t <= 0 {
			continue
		}
		if !w.IsAlive(tgt) || ecs.Has[components.Dead](w, tgt) || ecs.Has[components.Offline](w, tgt) {
			delete(c.Threats, tgt)
			changed = true
			continue
		}
		pp := ecs.Get[components.Position](w, tgt)
		if leash > 0 && !cp.WithinRange(*pp, leash) {
			delete(c.Threats, tgt)
			changed = true
			continue
		}
		if t > best {
			best, target = t, tgt
		}
	}
	// 清理仇恨表里已不可见/无效的残留（避免陈旧目标）
	for tgt := range c.Threats {
		if !w.IsAlive(tgt) || ecs.Has[components.Dead](w, tgt) || ecs.Has[components.Offline](w, tgt) {
			delete(c.Threats, tgt)
			changed = true
		}
	}
	if target != ai.Target {
		ai.Target = target
		changed = true
	}

	// 状态转移（每 tick 用当前输入评估）
	wp := weaponOf(w, e)
	switch {
	case target == 0:
		ai.State = components.CreatureIdle
	// 无攻击能力的被动生物（兔/鹿）被打后直接逃跑，不等血量掉到阈值
	//（M7 文档：兔子"被打会逃跑"、鹿"被动低血逃跑"——统一走 flee）
	case wp.AttackDamage <= 0 || (ai.FleeHP > 0 && hp.Cur <= ai.FleeHP):
		ai.State = components.CreatureFlee
	case wp.AttackDamage > 0 && cp.WithinRange(*ecs.Get[components.Position](w, target), wp.AttackRange):
		ai.State = components.CreatureAttack
	default:
		ai.State = components.CreatureChase
	}

	switch ai.State {
	case components.CreatureIdle:
		changed = s.idle(w, e, c, cp, now) || changed
	case components.CreatureChase:
		changed = s.chase(w, e, ai, cp) || changed
	case components.CreatureAttack:
		changed = s.attack(w, e, ai) || changed
	case components.CreatureFlee:
		changed = s.flee(w, e, ai, cp) || changed
	}
	if changed {
		ecs.MarkDirty[components.AI](w, e)
	}
}

// isHostile 该实体是否被生物视为敌对：玩家看 HostilePlayers 配置，生物看 HostileKinds。
func isHostile(w *ecs.World, ai *components.AI, v ecs.Entity) bool {
	if ecs.Has[components.Player](w, v) {
		return ai.HostilePlayers
	}
	if ecs.Has[components.Creature](w, v) {
		k := ecs.Get[components.Creature](w, v).Kind
		for _, h := range ai.HostileKinds {
			if h == k {
				return true
			}
		}
	}
	return false
}

// aoiVisible 取实体视野内的对象（无 AOI = 空；Visible 已按实体 id 升序）。
func aoiVisible(w *ecs.World, e ecs.Entity) []ecs.Entity {
	if !ecs.Has[components.AOI](w, e) {
		return nil
	}
	return ecs.Get[components.AOI](w, e).Visible
}

// idle 待机/游荡：围绕出生点，超半径回防；周期换向（hash 种子确定性）。
func (s *AISystem) idle(w *ecs.World, e ecs.Entity, c *components.Creature, cp *components.Position, now int) bool {
	if c.RoamRadius <= 0 {
		return false
	}
	home := components.Position{X: c.HomeX, Y: c.HomeY}
	if cp.Manhattan(home) > c.RoamRadius {
		return enqueueMove(w, e, signOf(c.HomeX-cp.X), signOf(c.HomeY-cp.Y))
	}
	if (now+int(e))%24 != 0 {
		return false
	}
	seed := uint64(now) ^ uint64(e)*0x9E3779B97F4A7C15
	dx := int(splitmix(seed)%3) - 1
	dy := int(splitmix(seed^0xBF58476D1CE4E5B9)%3) - 1
	if dx == 0 && dy == 0 {
		return false
	}
	return enqueueMove(w, e, dx, dy)
}

// chase 追击：寻路/贪心朝目标移动（路径写入 Moveable.Queue 由 MoveSystem 消费）。
func (s *AISystem) chase(w *ecs.World, e ecs.Entity, ai *components.AI, cp *components.Position) bool {
	tp := ecs.Get[components.Position](w, ai.Target)
	mv := ecs.Get[components.Moveable](w, e)
	if len(mv.Queue) > 0 {
		return false // 队列未走完，等 MoveSystem 消费
	}
	if wg, ok := ecs.TryResource[worldmap.WalkGrid](w); ok {
		if path := worldmap.FindPath(wg, cp.X, cp.Y, tp.X, tp.Y); len(path) > 0 {
			if len(path) > 16 {
				path = path[:16]
			}
			mv.Queue = append(mv.Queue, path...)
			ecs.MarkDirty[components.Moveable](w, e)
			return true
		}
		return false // 有地图但不可达：不贪心下水
	}
	return enqueueMove(w, e, signOf(tp.X-cp.X), signOf(tp.Y-cp.Y))
}

// attack 攻击：冷却结束且在范围内 → 统一攻击结算（ApplyAttack 写受击标记/仇恨/打断）。
func (s *AISystem) attack(w *ecs.World, e ecs.Entity, ai *components.AI) bool {
	if ai.Target == 0 {
		return false
	}
	wp := weaponOf(w, e)
	if wp.AttackDamage <= 0 {
		return false
	}
	if ai.Cooldown > 0 {
		ai.Cooldown--
		return true
	}
	ApplyAttack(w, e, ai.Target, wp.AttackDamage)
	ai.Cooldown = wp.AttackCooldown
	return true
}

// flee 逃跑：远离威胁目标（FleeDir 校验可走方向），无地图退化为反向直走。
func (s *AISystem) flee(w *ecs.World, e ecs.Entity, ai *components.AI, cp *components.Position) bool {
	if ai.Target == 0 {
		return false
	}
	tp := ecs.Get[components.Position](w, ai.Target)
	mv := ecs.Get[components.Moveable](w, e)
	if len(mv.Queue) > 0 {
		return false
	}
	dx, dy := 0, 0
	if wg, ok := ecs.TryResource[worldmap.WalkGrid](w); ok {
		dx, dy = worldmap.FleeDir(wg, cp.X, cp.Y, tp.X, tp.Y)
	} else {
		dx, dy = signOf(cp.X-tp.X), signOf(cp.Y-tp.Y)
	}
	return enqueueMove(w, e, dx, dy)
}

// ApplyAttack 统一攻击结算（玩家命令 / 生物 AI 共用）：
// 扣血 + 受击标记（LastHitBy，tick 窗口）+ 仇恨（被打生物记仇）+ 受击打断（制作等）。
func ApplyAttack(w *ecs.World, attacker, target ecs.Entity, damage int) {
	if damage <= 0 || !w.IsAlive(target) || ecs.Has[components.Dead](w, target) || !ecs.Has[components.Health](w, target) {
		return
	}
	hp := ecs.Get[components.Health](w, target)
	hp.Cur -= damage
	if hp.Cur < 0 {
		hp.Cur = 0
	}
	ecs.MarkDirty[components.Health](w, target)
	// 受击标记（AI 输入：本 tick 被谁打）
	if ecs.Has[components.AI](w, target) {
		ai := ecs.Get[components.AI](w, target)
		ai.LastHitBy = attacker
		ai.LastHitAt = worldPhase(w)
		ecs.MarkDirty[components.AI](w, target)
	}
	// 仇恨（被打的生物记仇）
	if ecs.Has[components.Creature](w, target) {
		c := ecs.Get[components.Creature](w, target)
		if c.Threats == nil {
			c.Threats = map[ecs.Entity]int32{}
		}
		c.Threats[attacker] += int32(damage)
		ecs.MarkDirty[components.Creature](w, target)
	}
	// 受击打断（制作等）
	components.TryInterrupt(w, target)
}

// weaponOf 取实体攻击能力（无 Weapon 组件 = 徒手，无法攻击）。
func weaponOf(w *ecs.World, e ecs.Entity) components.Weapon {
	if ecs.Has[components.Weapon](w, e) {
		return *ecs.Get[components.Weapon](w, e)
	}
	return components.Weapon{}
}

// enqueueMove 往 Moveable.Queue 压一步（有界），返回是否真的压入。
func enqueueMove(w *ecs.World, e ecs.Entity, dx, dy int) bool {
	if dx == 0 && dy == 0 {
		return false
	}
	mv := ecs.Ensure[components.Moveable](w, e)
	if len(mv.Queue) >= 16 {
		return false
	}
	mv.Queue = append(mv.Queue, components.MoveDir{DX: dx, DY: dy})
	ecs.MarkDirty[components.Moveable](w, e)
	return true
}

func signOf(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

// worldPhase 世界时钟（DayCycle.Phase 每 tick 递增，作为确定性时间轴）。
func worldPhase(w *ecs.World) int {
	return ecs.Resource[components.DayCycle](w).Phase
}

// splitmix 确定性伪随机（无共享状态，同种子同值）。
func splitmix(seed uint64) uint64 {
	seed ^= seed >> 30
	seed *= 0xBF58476D1CE4E5B9
	seed ^= seed >> 27
	seed *= 0x94D049BB133111EB
	seed ^= seed >> 31
	return seed
}
