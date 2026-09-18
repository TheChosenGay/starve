package behavior

// 本文件是 **Boss 专用节点**：多阶段决策 + 投弹/闪现/连拳/AOE 这类
// 比普通生物复杂得多的行为。
//
// 设计原则与本包一致：节点只产出"意图"（通过 Env），不直接改世界状态。
// 跨 tick 的进度（阶段、连招计数、一次性标记）一律存进 NodeStateStore
// 或黑板，保证可存档、可重放。

// --- 条件节点 ---

// PhaseIs 条件：当前阶段是否等于指定值。
//
// 阶段是**持久化**在黑板（AI 组件）上的，所以阶段一旦切换就保持，
// 行为树每 tick 重新评估也能得到稳定结果。
type PhaseIs struct {
	nodeBase
	Phase int
}

// NewPhaseIs 构造阶段判断条件。
func NewPhaseIs(phase int) *PhaseIs { return &PhaseIs{Phase: phase} }

func (PhaseIs) Children() []Node { return nil }

func (n *PhaseIs) Tick(d *TickContext, _ NodeID) Status {
	if d.Board.Phase() == n.Phase {
		return Success
	}
	return Failure
}

// EnterPhase 动作：把黑板阶段切到指定值（并返回 Success）。
//
// 与 PhaseIs 配合构成"阶段推进"：放在 Selector 靠前的位置，
// 满足条件时先切阶段，后续分支就会走新阶段的逻辑。
type EnterPhase struct {
	nodeBase
	Phase int
}

// NewEnterPhase 构造阶段切换动作。
func NewEnterPhase(phase int) *EnterPhase { return &EnterPhase{Phase: phase} }

func (EnterPhase) Children() []Node { return nil }

func (n *EnterPhase) Tick(d *TickContext, _ NodeID) Status {
	if d.Board.Phase() != n.Phase {
		d.Board.SetPhase(n.Phase)
	}
	return Success
}

// Phase2Ready 条件：血量已降到二阶段阈值（且配置了阈值）。
type Phase2Ready struct{ nodeBase }

func (Phase2Ready) Children() []Node { return nil }

func (Phase2Ready) Tick(d *TickContext, _ NodeID) Status {
	threshold := d.Board.Phase2HP()
	if threshold <= 0 {
		return Failure
	}
	if d.Board.Health() <= threshold {
		return Success
	}
	return Failure
}

// HasTargetInRange 条件：目标存在且距离 <= r（比 InAttackRange 更通用的距离判断）。
type HasTargetInRange struct {
	nodeBase
	Range int
}

// NewHasTargetInRange 构造距离条件。
func NewHasTargetInRange(r int) *HasTargetInRange { return &HasTargetInRange{Range: r} }

func (HasTargetInRange) Children() []Node { return nil }

func (n *HasTargetInRange) Tick(d *TickContext, _ NodeID) Status {
	if d.Board.Target() == 0 {
		return Failure
	}
	dist := d.Board.DistanceToTarget()
	if dist < 0 || dist > n.Range {
		return Failure
	}
	return Success
}

// NotBusy 条件：当前没有动作在进行（可以安全插入新动作）。
type NotBusy struct{ nodeBase }

func (NotBusy) Children() []Node { return nil }

func (NotBusy) Tick(d *TickContext, _ NodeID) Status {
	if d.Board.Busy() {
		return Failure
	}
	return Success
}

// --- 动作节点 ---

// BossAbility 是 Boss 技能标识（**决策层的概念**，不依赖组件/协议类型）。
//
// 为什么不在本包里直接用 components.ActionKind：behavior 包刻意保持"零组件依赖"
// （见 blackboard.go 顶部的分工说明），技能 → 协议动作类型的映射放在 systems 桥接层
// （behavior_bridge.go 的 bossAbilityActionKind）。
type BossAbility uint8

const (
	BossAbilityThrow BossAbility = iota + 1 // 投弹
	BossAbilityLeap                         // 闪现突进
	BossAbilitySlam                         // 锤地 AOE
	BossAbilityRoar                         // 嚎叫
)

// String 便于日志/调试可读。
func (a BossAbility) String() string {
	switch a {
	case BossAbilityThrow:
		return "throw"
	case BossAbilityLeap:
		return "leap"
	case BossAbilitySlam:
		return "slam"
	case BossAbilityRoar:
		return "roar"
	}
	return "unknown"
}

// 投弹/闪现的"表现时长"（tick）：只决定客户端播多久动画，**不参与任何判定**。
//
// 为什么这两个是常量而 slam/roar 用节点自己的前摇：投弹与闪现的效果是**瞬时**的
// （炸弹立刻离手 / 立刻位移），没有可对齐的前摇；给它们一个固定的动画时长，
// 只是让客户端"有个动作可播"。slam/roar 的效果有真实前摇，直接用那个值（单一来源）。
const (
	bossThrowAnimTicks = 12
	bossLeapAnimTicks  = 8
)

// ThrowBombAction 动作：朝目标投一枚炸弹。
//
// 节流：投弹**不经过动作时间轴**（没有 ActionState），所以 Busy() 永远是 false，
// 光靠它会每 tick 投一发——实测同时 23 颗炸弹在飞、日志被"炸弹命中玩家"
// 刷屏（把二阶段的拳/砸日志全顶掉了）。因此这里用投掷间隔自己节流：
// 距上次投掷不足 IntervalTicks 就返回 Running（表示"还在冷却/上一发还在飞"）。
//
// IntervalTicks <= 0 时退化为不节流（每 tick 投），仅用于测试。
type ThrowBombAction struct {
	nodeBase
	IntervalTicks int
}

// NewThrowBomb 构造投弹动作（intervalTicks <= 0 用缺省 20 = 1 秒 @20Hz）。
func NewThrowBomb(intervalTicks int) *ThrowBombAction {
	return &ThrowBombAction{IntervalTicks: intervalTicks}
}

func (ThrowBombAction) Children() []Node { return nil }

func (n *ThrowBombAction) Tick(d *TickContext, id NodeID) Status {
	target := d.Board.Target()
	if target == 0 {
		return Failure
	}
	if d.Board.Busy() {
		return Running // 有权威动作在进行
	}
	interval := n.IntervalTicks
	if interval <= 0 {
		interval = 20
	}
	// 冷却中：递减并等待，不投弹。
	if left := d.State.IntOf(id); left > 0 {
		d.State.SetIntOf(id, left-1)
		return Running
	}
	d.Env.BossWindup(BossAbilityThrow, bossThrowAnimTicks)
	d.Env.ThrowBomb(target)
	d.State.SetIntOf(id, interval)
	return Success
}

// RoarAction 动作：嚎叫一次（表现 + 后续可用于群体仇恨广播）。
//
// 用 Running 表达"嚎叫有持续时间"：嚎叫期间 Boss 不做别的，
// 由 roarTicks 计数控制时长（存进 NodeStateStore，可存档）。
type RoarAction struct {
	nodeBase
	Ticks int
}

// NewRoar 构造嚎叫动作（ticks <= 0 用缺省 30 = 1.5 秒 @20Hz）。
func NewRoar(ticks int) *RoarAction { return &RoarAction{Ticks: ticks} }

func (RoarAction) Children() []Node { return nil }

func (n *RoarAction) Tick(d *TickContext, id NodeID) Status {
	total := n.Ticks
	if total <= 0 {
		total = 30
	}
	elapsed := d.State.IntOf(id)
	if elapsed == 0 {
		// 嚎叫的动作时长就是节点的时长 ⇒ 客户端动画与"嚎叫期间不做别的"完全对齐
		d.Env.BossWindup(BossAbilityRoar, total)
		d.Env.Roar() // 第一 tick 触发表现
	}
	if elapsed >= total {
		d.State.SetIntOf(id, 0) // 结束，清零供下次使用
		return Success
	}
	d.State.SetIntOf(id, elapsed+1)
	return Running
}

// LeapToTargetAction 动作：瞬间位移到目标身边（闪现）。
//
// 语义：这是一次**原子**操作（成功 = 已经到目标身边），
// 不需要跨 tick 的飞行过程——符合"阶段二瞬间位移"的设计。
// 闪现后若仍在动作中（Busy）则返回 Running，等动作结束再交还控制权。
type LeapToTargetAction struct{ nodeBase }

func (LeapToTargetAction) Children() []Node { return nil }

func (LeapToTargetAction) Tick(d *TickContext, _ NodeID) Status {
	target := d.Board.Target()
	if target == 0 {
		return Failure
	}
	// 位移本身是瞬时的，动画时长只是给客户端一个"闪现"可播（见 bossLeapAnimTicks）。
	// 只在动作槽空闲时声明：忙的时候那一次会被控制系统拒掉，此时**仍然照常闪现**
	// （位移是玩法，动画是表现，不能让表现挡住玩法）。
	if !d.Env.ActionBusy() {
		d.Env.BossWindup(BossAbilityLeap, bossLeapAnimTicks)
	}
	if d.Env.LeapTo(target) {
		return Success
	}
	return Failure
}

// PunchAction 动作：对目标打一拳（近战）。
//
// 与 AttackAction 的区别：Punch 是 Boss 的连招单元，配合 Counter 计数；
// 是否命中由底层动作结算，Counter 只数"成功发起了几次"。
//
// 冷却：出拳必须**尊重攻击冷却**（AttackReady）。早期实现只检查
// ActionBusy()，而攻击动作结束很快，于是三拳在同一 tick 内连着打完
// （实测相邻两拳间隔 1 tick），既没有打击节奏、也让"三拳一砸"看着像
// 一瞬间的事。现在冷却未好时返回 Running（继续等），由 AI.Cooldown
// 每 tick 递减（见 AISystem.runTree）。
type PunchAction struct{ nodeBase }

func (PunchAction) Children() []Node { return nil }

func (PunchAction) Tick(d *TickContext, _ NodeID) Status {
	target := d.Board.Target()
	if target == 0 {
		return Failure
	}
	if d.Env.ActionBusy() {
		return Running // 上一拳的动作还没打完
	}
	if !d.Env.AttackReady() {
		return Running // 还在冷却：等，不推进连招计数
	}
	d.Env.Punch(target)
	return Success
}

// SlamAOEAction 动作：锤地面，以自身为中心释放范围攻击。
//
// 两个时间参数：
//   - Ticks（前摇）：从起手到 AOE 真正打出去的时间。期间返回 Running，
//     Boss 定住不动——给玩家反应/闪避的窗口。
//   - RecoverTicks（后摇）：AOE 打完之后到"可以再做下一个动作"的间隔。
//     没有它的话，三拳一砸会连着放，节奏糊成一团。
//
// 状态机（用节点计数器存，可存档）：
//
//	elapsed == 0            → 进入前摇
//	0 < elapsed < Ticks     → 前摇中（Running）
//	elapsed == Ticks        → **打出 AOE**（结算伤害）
//	elapsed < Ticks+Recover → 后摇中（Running，不能接其他动作）
//	否则                    → Success，清零
type SlamAOEAction struct {
	nodeBase
	Ticks        int
	RecoverTicks int
}

// NewSlamAOE 构造 AOE 动作。
// ticks <= 0 用缺省 20（1 秒前摇）；recoverTicks <= 0 用缺省 10（0.5 秒后摇）。
func NewSlamAOE(ticks, recoverTicks int) *SlamAOEAction {
	return &SlamAOEAction{Ticks: ticks, RecoverTicks: recoverTicks}
}

func (SlamAOEAction) Children() []Node { return nil }

func (n *SlamAOEAction) Tick(d *TickContext, id NodeID) Status {
	windup := n.Ticks
	if windup <= 0 {
		windup = 20
	}
	recover := n.RecoverTicks
	if recover <= 0 {
		recover = 10
	}
	elapsed := d.State.IntOf(id)
	// 起手声明（客户端据此播技能动画）：前摇期间**只要动作槽是空的就发**。
	//
	// 为什么不是"第 0 tick 发一次就完"：锤地紧跟在三拳之后，而上一拳的动作
	// （含 recovery）常常还没结束 ⇒ 那唯一一次声明会被控制系统以"动作忙"拒掉，
	// 整次锤地就没有动画（实测：三拳后的第一次锤地无声无息）。
	// 改成空槽就发、且**时长取剩余前摇**：无论早发晚发，
	// 动作结束的那一刻都正好是下面打出 AOE 的那一刻。
	if elapsed < windup && !d.Env.ActionBusy() {
		d.Env.BossWindup(BossAbilitySlam, windup-elapsed)
	}
	// 前摇走完的那一 tick 打出 AOE（只打一次）
	if elapsed == windup {
		d.Env.SlamAOE()
	}
	if elapsed >= windup+recover {
		d.State.SetIntOf(id, 0)
		return Success
	}
	d.State.SetIntOf(id, elapsed+1)
	return Running
}
