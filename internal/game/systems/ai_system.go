package systems

import (
	"sort"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/behavior"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/worldmap"
)

// AISystem 生物决策系统（order 92，感知之后、移动之前）：
//
// 职责分工（行为树重构后）：
//   - **本系统**：感知结算（仇恨衰减/累加/选目标）+ 每 tick 驱动一次行为树；
//   - **行为树**（internal/game/behavior）：全部决策逻辑（逃跑/攻击/追击/游荡）。
//
// 输入 = AOI.Visible（感知）+ LastHitBy（受击窗口）+ HP + 距离；
// 输出 = ControlQueue 中的移动/攻击控制意图；不直接改位移或结算伤害。
//
// 对外状态 AI.State（idle/chase/attack/flee）是行为树结果的**投影**，
// 保留它是为了不改客户端协议（客户端按 state 做动画，见 M7 对接文档）。
//
// 没有 BehaviorTree 组件的实体（旧存档）自动走 legacyDecide 回退路径。
//
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

	// 决策：交给行为树（原硬编码 4 状态 switch 已等价迁移到 behavior 包）。
	//
	// 分工：本函数负责"感知 + 仇恨 + 选目标"，**决策与动作**全部由行为树表达。
	// 树执行时会通过 btEnv 提交控制意图（移动/攻击），响应慢的动作用 Running 跨 tick。
	changed = s.runTree(w, e, ai) || changed
	if changed {
		ecs.MarkDirty[components.AI](w, e)
	}
}

// runTree 驱动实体的行为树，并把结果映射回 AI.State（供快照/客户端表现）。
//
// 为什么还要维护 AI.State：客户端已经按 state 做动画（见 M7 对接文档 §2），
// 且它是快照契约的一部分。行为树是**权威决策**，state 是它的**对外投影**——
// 两者保持一致，就不必改客户端协议。
func (s *AISystem) runTree(w *ecs.World, e ecs.Entity, ai *components.AI) bool {
	changed := false
	bt := behaviorTreeOf(w, e)
	if bt == nil {
		// 没有行为树组件：退化为原有的直接状态机（保证旧存档/测试仍可跑）。
		return s.legacyDecide(w, e, ai)
	}
	tree := treeForIn(w, bt.Kind)
	if tree == nil {
		return s.legacyDecide(w, e, ai)
	}
	// 攻击冷却倒计时：原实现写在 attack() 里（只有走攻击分支才递减），
	// 现在决策交给行为树，倒计时必须在这里统一维护——否则 AI.Cooldown
	// 永远不归零，生物打完一下就再也不攻击了（ControlSystem 在接纳攻击时
	// 把它置为 AttackCooldown，见 control_system.go）。
	if ai.Cooldown > 0 {
		ai.Cooldown--
		changed = true
	}
	board := newBoard(w, e)
	ctx := behavior.NewTickContext(board, newEnv(w, e), bt, uint64(e))
	tree.Tick(ctx)

	// 结果投影：按黑板当前状态推断"表现状态"。
	// 注意这是**派生**的，不参与决策——决策已经在树里做完了。
	state := projectedState(board)
	if ai.State != state {
		ai.State = state
		changed = true
	}
	return changed
}

// projectedState 把黑板状态投影成对外的 4 态表现（idle/chase/attack/flee）。
//
// 映射规则与行为树的优先级一致（逃跑 > 攻击 > 追击 > 待机），
// 保证客户端看到的 state 与 AI 实际在做的事吻合。
func projectedState(b behavior.Blackboard) components.CreatureState {
	target := b.Target()
	if target == 0 {
		return components.CreatureIdle
	}
	if b.FleeHP() > 0 && b.Health() <= b.FleeHP() {
		return components.CreatureFlee
	}
	if b.AttackDamage() <= 0 {
		return components.CreatureFlee // 被动生物有目标即逃（与 PreyTree 一致）
	}
	if b.InAttackRange() {
		return components.CreatureAttack
	}
	return components.CreatureChase
}

// legacyDecide 是无行为树组件时的回退路径：保留原 4 状态状态机语义。
//
// 存在的意义：行为树组件是**新增**的，旧存档里没有它；回退保证读旧档
// 的生物仍然会动，而不是站着不动。新生成/迁移后的实体一律走行为树。
func (s *AISystem) legacyDecide(w *ecs.World, e ecs.Entity, ai *components.AI) bool {
	cp := ecs.Get[components.Position](w, e)
	hp := ecs.Get[components.Health](w, e)
	now := worldPhase(w)
	changed := false

	wp := weaponOf(w, e)
	switch {
	case ai.Target == 0:
		ai.State = components.CreatureIdle
	case wp.AttackDamage <= 0 || (ai.FleeHP > 0 && hp.Cur <= ai.FleeHP):
		ai.State = components.CreatureFlee
	case wp.AttackDamage > 0 && cp.WithinRange(*ecs.Get[components.Position](w, ai.Target), wp.AttackRange):
		ai.State = components.CreatureAttack
	default:
		ai.State = components.CreatureChase
	}
	c := ecs.Get[components.Creature](w, e)
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
	return changed
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
		return setAIPath(w, e, []components.MoveDir{{DX: signOf(c.HomeX - cp.X), DY: signOf(c.HomeY - cp.Y)}})
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
	return setAIPath(w, e, []components.MoveDir{{DX: dx, DY: dy}})
}

// chase 追击：寻路/贪心朝目标移动（路径写入 Moveable.Path，MoveSystem 连续跟随）。
func (s *AISystem) chase(w *ecs.World, e ecs.Entity, ai *components.AI, cp *components.Position) bool {
	tp := ecs.Get[components.Position](w, ai.Target)
	mv := ecs.Get[components.Moveable](w, e)
	if len(mv.Path) > 0 {
		return false // 路径未走完，MoveSystem 连续跟随
	}
	if md, ok := ecs.TryResource[worldmap.MapData](w); ok {
		if path := worldmap.FindPath(md, cp.X, cp.Y, tp.X, tp.Y); len(path) > 0 {
			if len(path) > 16 {
				path = path[:16]
			}
			return setAIPath(w, e, path)
		}
		return false // 有地图但不可达：不贪心下水
	}
	return setAIMove(w, e, signOf(tp.X-cp.X), signOf(tp.Y-cp.Y))
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
	if ecs.Has[components.ActionState](w, e) {
		return false
	}
	EnqueueControl(w, StartActionIntent(e, components.ActionAttack, ai.Target, 0, 0))
	return false
}

// flee 逃跑：远离威胁目标（FleeDir 校验可走方向），无地图退化为反向直走。
func (s *AISystem) flee(w *ecs.World, e ecs.Entity, ai *components.AI, cp *components.Position) bool {
	if ai.Target == 0 {
		return false
	}
	tp := ecs.Get[components.Position](w, ai.Target)
	dx, dy := 0, 0
	if md, ok := ecs.TryResource[worldmap.MapData](w); ok {
		dx, dy = worldmap.FleeDir(md, cp.X, cp.Y, tp.X, tp.Y)
	} else {
		dx, dy = signOf(cp.X-tp.X), signOf(cp.Y-tp.Y)
	}
	return setAIMove(w, e, dx, dy)
}

// weaponOf 取实体攻击能力（Attacker，-er）；无则徒手（无法攻击）。
func weaponOf(w *ecs.World, e ecs.Entity) interactive.Attacker {
	if ecs.Has[interactive.Attacker](w, e) {
		return *ecs.Get[interactive.Attacker](w, e)
	}
	return interactive.Attacker{}
}

// setAIMove 提交 AI 连续移动方向，由 ControlSystem 统一仲裁。
func setAIMove(w *ecs.World, e ecs.Entity, dx, dy int) bool {
	if dx == 0 && dy == 0 {
		return false
	}
	EnqueueControl(w, MoveIntent(e, dx, dy, 0))
	return true
}

// setAIPath 提交 AI 路径，由 ControlSystem 统一仲裁。
func setAIPath(w *ecs.World, e ecs.Entity, path []components.MoveDir) bool {
	if len(path) == 0 {
		return false
	}
	EnqueueControl(w, PathIntent(e, path, 0))
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
