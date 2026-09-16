package systems

import (
	"sort"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/behavior"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
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
// 决策完全由行为树承担；旧存档由 save.go 的 migrateBehaviorTrees 补挂行为树。
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
		// 没有行为树组件：不决策（不发移动/攻击意图）。
		//
		// 这里**不再回退**到旧状态机——旧的 4 状态 switch 已删除，行为树是
		// 唯一决策来源。旧存档由 migrateBehaviorTrees 在读档时补挂行为树
		// （见 save.go），新生成的实体在 seedCreatures 里就已经挂好。
		// 真出现"有 AI 却没树"的实体，多半是漏挂组件的 bug，
		// 此时保持静止比偷偷走另一套语义更容易被发现。
		return false
	}
	tree := treeForIn(w, bt.Kind)
	if tree == nil {
		return false
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
