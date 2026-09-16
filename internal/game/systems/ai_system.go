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
	// 护栏：Creature 与 AI 是两个组件，**不保证同时存在**。
	//
	// 现实内容里"只有 Creature、没有 AI"的实体是合法的：装饰性生物、
	// 训练木桩、只作为仇恨对象存在但不参与决策的目标。
	// 没有这个护栏，`ecs.Get[AI]` 会直接 panic 掉整个服务器
	// （实测：加一个这样的实体就能让世界 tick 崩溃）。
	if !ecs.Has[components.AI](w, e) {
		return
	}
	// Position 同理：没有它连"我在哪"都不知道，无法决策。
	if !ecs.Has[components.Position](w, e) {
		return
	}
	c := ecs.Get[components.Creature](w, e)
	ai := ecs.Get[components.AI](w, e)
	cp := ecs.Get[components.Position](w, e)
	now := worldPhase(w)
	changed := false

	// ── 仇恨结算（三条规则）──────────────────────────────────
	//
	//  ① 直接仇恨：谁亲自打我。不可被传播覆盖；新的亲自攻击者会更新它。
	//  ② 间接仇恨：同伴被打后传播来的，**随距离反向**且随时重算；
	//     多个来源时按距离选最近的；被亲自攻击（升级为直接）或来了更近的
	//     来源 → 覆盖当前。
	//  ③ 感知范围内既无敌对目标、也无间接仇恨对象 → 清空仇恨。
	//
	// 本函数只维护 Direct/Indirect 这两个语义字段；Threats 数值表是它们的
	// **协议投影**（供快照与存档），不参与决策。

	// 拴绳（放弃追击的距离）：优先用模板配置，否则退化为 4 + 感知半径。
	//
	// 为什么需要显式配置：缺省规则把拴绳绑死在感知半径上——狼的感知半径
	// 只有 6，拴绳因此仅 10 格。实测"打一下、退两步"（距离 7 格）狼就
	// 清空仇恨不追了，玩家会觉得"攻击了也不追"。掠食者应当闻着血腥味
	// 追得更远，所以按生物类型单独给（见 creatures.json 的 leash）。
	leash := ai.Leash
	if leash <= 0 {
		leash = 4
		if ecs.Has[components.AOI](w, e) {
			leash += ecs.Get[components.AOI](w, e).Radius
		}
	}

	// 感知范围内的敌对目标：**看见即视为直接仇恨**。
	//
	// 为什么"看见"也算直接仇恨：狼看到兔子就该扑上去捕猎，这与"它打了我"
	// 在决策上是同一件事——都是"我当前的敌人是谁"。所以不需要为感知另开
	// 一条路径，也不需要旧实现的"每 tick +1 累积仇恨"（那个累积正是导致
	// 仇恨值失真、进而需要和传播值比大小的根源）。
	//
	// 语义仍是"直接仇恨 = 当前敌人"，因此它同样**不可被传播来的目标覆盖**：
	// 狼不会因为远处的同伴挨打，就放着眼前的兔子不管。
	//  注意用 **InPerception** 复核，不能直接用 Visible：
	//  Visible 的半径是 max(感知, 仇恨传播)，是个超集；若不做复核，
	//  "看见敌人"会被放大到仇恨传播范围（狼隔着 16 格就扑过来），潜行失效。
	visibleEnemy := ecs.Entity(0)
	for _, v := range aoiVisible(w, e) {
		if !isHostile(w, ai, v) {
			continue
		}
		if !w.IsAlive(v) || ecs.Has[components.Dead](w, v) || ecs.Has[components.Offline](w, v) {
			continue
		}
		if !inPerception(w, e, v) {
			continue
		}
		visibleEnemy = v // Visible 已按实体 id 升序，取第一个即可（确定性）
		break
	}

	// ── 规则 ①：直接仇恨的维护 ────────────────────────────────
	// 两个来源，优先级：**正在打我的人 > 视野内的敌对目标**。
	//
	//   - LastHitBy（受击窗口内）是"刚刚谁打我"，由 ApplyDamage 写入，
	//     是更强的实时信号；它存在时以它为准。
	//   - 否则用视野内的敌对目标（看见即直接仇恨，见上）。
	//
	// SetDirectThreat 是赋值语义：新的敌人顶掉旧的。
	// 这恰好实现了你要的"另一个对象攻击了我就会更新直接仇恨"。
	switch {
	case ai.WasHitRecently(now) && ai.LastHitBy != 0:
		if c.Direct != ai.LastHitBy {
			c.SetDirectThreat(w, e, ai.LastHitBy)
			changed = true
		}
	case visibleEnemy != 0:
		if c.Direct != visibleEnemy {
			c.SetDirectThreat(w, e, visibleEnemy)
			changed = true
		}
	}

	// 直接仇恨目标死亡 / 离线 → 清除（规则 ③ 的一部分）
	if d := c.Direct; d != 0 {
		if !w.IsAlive(d) || ecs.Has[components.Dead](w, d) || ecs.Has[components.Offline](w, d) {
			c.DropThreat(w, e, d)
			changed = true
		}
	}

	// ── 规则 ②：间接仇恨按距离重算 ──────────────────────────
	// 距离是"随时会变"的量：每个 tick 按当前位置刷新。
	// 目标跑远（超拴绳/超上限）→ 直接移除（规则 ③）。
	//
	// 复制一份 key 再遍历：循环体会改 map（删除），直接 range 会踩 Go 的
	// "边遍历边删"语义（结果不确定）。
	for _, src := range indirectSources(c) {
		if !w.IsAlive(src) || ecs.Has[components.Dead](w, src) || ecs.Has[components.Offline](w, src) {
			c.DropThreat(w, e, src)
			changed = true
			continue
		}
		// 被亲自攻击过 → 升级为直接仇恨，间接记录作废（规则 ②的"覆盖"）
		if c.Direct == src {
			c.DropThreat(w, e, src)
			changed = true
			continue
		}
		sp := ecs.Get[components.Position](w, src)
		dist := chebyshevInt(cp.X, cp.Y, sp.X, sp.Y)
		if leash > 0 && dist > leash {
			c.DropThreat(w, e, src)
			changed = true
			continue
		}
		if dist > components.MaxIndirectDistance {
			c.DropThreat(w, e, src)
			changed = true
			continue
		}
		// 规则 ②：距离随位置变化**随时重算**（近处优先级高）。
		if c.Indirect[src] != dist {
			c.Indirect[src] = dist
			c.SyncThreats(w, e)
			changed = true
		}
	}

	// ── 选目标：直接仇恨 > 间接仇恨（绝不是比数值大小）────────
	//
	//   ① 有直接仇恨 → 就是它（不可被任何传播来的目标替换）
	//   ② 否则 → 间接仇恨里**距离最近**的那个（越近优先级越高）
	//
	// 为什么不能把所有仇恨丢进一个数值池比大小：群体仇恨按伤害分摊，
	// 多个同伴被同一人打时叠加值可轻易超过自击者。对抗性实测：
	// 正在打我的玩家 仇恨=1，通知来的玩家 仇恨=125 → 纯比数值会让生物
	// **抛下正在揍它的敌人**去打远处的。语义分级后这种淹没不可能发生。
	target := ecs.Entity(0)
	if d := c.Direct; d != 0 {
		target = d
	} else {
		bestDist := 1 << 30
		for _, src := range indirectSources(c) {
			if dist, ok := c.Indirect[src]; ok && dist < bestDist {
				bestDist, target = dist, src
			}
		}
	}

	// ── 规则 ③（清空）：既无可见敌对目标，也无任何仇恨 → 遗忘 ──
	// 放在选目标之后，保证 "Direct/Indirect 都空" 才触发。
	if target == 0 && !c.HasAnyThreat() && visibleEnemy == 0 {
		c.ClearThreats(w, e)
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
	if bt == nil || treeForIn(w, bt.Kind) == nil {
		// 没有行为树（或 Kind 无效）= 没有决策来源。
		//
		// 不回退到旧状态机——旧的 4 状态 switch 已彻底删除，行为树是**唯一**
		// 决策来源。生物该挂树的地方只有两处：seedCreatures（新生成）与
		// 读档恢复；漏了就是 bug，此时"站着不动"比偷偷走另一套语义更容易发现。
		//
		// 兼容性说明：本次重构**不兼容旧存档**（旧档生物没有 BehaviorTree，
		// 读出来会静止不动）。原型阶段不做存档迁移，详见 save.go 的 SaveVersion 注释。
		return false
	}
	tree := treeForIn(w, bt.Kind)
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

// inPerception 报告 target 是否在 self 的**感知半径**内。
//
// 必须复核而不能直接用 AOI.Visible：Visible 的半径是
// max(感知, 仇恨传播)——为的是让仇恨传播覆盖更大范围（见 seed.go），
// 它是**超集**。直接拿它当"看得见"会让感知半径形同虚设。
func inPerception(w *ecs.World, self, target ecs.Entity) bool {
	if !ecs.Has[components.AOI](w, self) || !ecs.Has[components.Position](w, self) {
		return false
	}
	if !ecs.Has[components.Position](w, target) {
		return false
	}
	aoi := ecs.Get[components.AOI](w, self)
	sp := ecs.Get[components.Position](w, self)
	tp := ecs.Get[components.Position](w, target)
	return aoi.InPerception(*sp, *tp)
}

// indirectSources 返回当前间接仇恨的目标列表，**按实体 id 升序**。
//
// 复制成切片再遍历的原因：调用方会在循环里删除 map 元素（目标死亡/跑远），
// 直接 range + delete 在 Go 里语义不确定（可能漏掉或重复）。排序保证确定性。
func indirectSources(c *components.Creature) []ecs.Entity {
	if len(c.Indirect) == 0 {
		return nil
	}
	out := make([]ecs.Entity, 0, len(c.Indirect))
	for e := range c.Indirect {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// chebyshevInt 切比雪夫距离（max(|dx|,|dy|)）：与 AOI 的正方形感知口径一致。
func chebyshevInt(ax, ay, bx, by int) int {
	dx := ax - bx
	if dx < 0 {
		dx = -dx
	}
	dy := ay - by
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		return dy
	}
	return dx
}

// threatTargets 取仇恨表里的目标，按实体 id 升序（确定性）。
//
// 只返回 id（不含威胁值），调用方仍从 c.Threats 读取当前值。
func threatTargets(c *components.Creature) []ecs.Entity {
	if len(c.Threats) == 0 {
		return nil
	}
	out := make([]ecs.Entity, 0, len(c.Threats))
	for t := range c.Threats {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
