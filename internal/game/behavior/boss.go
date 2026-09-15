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

// ThrowBombAction 动作：朝目标投一枚炸弹。
//
// 返回 Running 表示已发起、还在飞（外层不应重复发起）。
type ThrowBombAction struct{ nodeBase }

func (ThrowBombAction) Children() []Node { return nil }

func (ThrowBombAction) Tick(d *TickContext, _ NodeID) Status {
	target := d.Board.Target()
	if target == 0 {
		return Failure
	}
	if d.Board.Busy() {
		return Running // 上一发还没落地
	}
	d.Env.ThrowBomb(target)
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
	if d.Env.LeapTo(target) {
		return Success
	}
	return Failure
}

// PunchAction 动作：对目标打一拳（近战）。
//
// 与 AttackAction 的区别：Punch 是 Boss 的连招单元，配合 Counter 计数；
// 是否命中由底层动作结算，Counter 只数"成功发起了几次"。
type PunchAction struct{ nodeBase }

func (PunchAction) Children() []Node { return nil }

func (PunchAction) Tick(d *TickContext, _ NodeID) Status {
	target := d.Board.Target()
	if target == 0 {
		return Failure
	}
	if d.Env.ActionBusy() {
		return Running // 上一拳还在打
	}
	d.Env.Punch(target)
	return Success
}

// SlamAOEAction 动作：锤地面，以自身为中心释放范围攻击。
//
// 用 Running 表达收招前摇：AOE 有起手时间，期间 Boss 定住不动，
// 由 slamTicks 计数控制（可存档）。
type SlamAOEAction struct {
	nodeBase
	Ticks int
}

// NewSlamAOE 构造 AOE 动作（ticks <= 0 用缺省 20 = 1 秒 @20Hz）。
func NewSlamAOE(ticks int) *SlamAOEAction { return &SlamAOEAction{Ticks: ticks} }

func (SlamAOEAction) Children() []Node { return nil }

func (n *SlamAOEAction) Tick(d *TickContext, id NodeID) Status {
	total := n.Ticks
	if total <= 0 {
		total = 20
	}
	elapsed := d.State.IntOf(id)
	if elapsed == 0 {
		d.Env.SlamAOE() // 起手：立即结算 AOE 意图
	}
	if elapsed >= total {
		d.State.SetIntOf(id, 0)
		return Success
	}
	d.State.SetIntOf(id, elapsed+1)
	return Running
}
