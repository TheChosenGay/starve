package behavior

// 本文件是**内置叶子节点库**：条件（只读黑板）与动作（写控制意图）。
//
// 设计约定：
//   - 条件节点永不返回 Running（决策是瞬时的，不该跨 tick 挂着）；
//   - 动作节点可以返回 Running（例如"攻击"要等冷却/动作完成）；
//   - 所有节点通过黑板/Env 访问世界，不直接碰 ECS。

// --- 条件节点 ---

// HasTarget 条件：当前是否锁定了目标。
type HasTarget struct{ nodeBase }

func (HasTarget) Children() []Node { return nil }

func (HasTarget) Tick(d *TickContext, _ NodeID) Status {
	if d.Board.Target() == 0 {
		return Failure
	}
	return Success
}

// LowHP 条件：血量是否已到逃跑阈值（FleeHP <= 0 视为"永不逃跑" = Failure）。
type LowHP struct{ nodeBase }

func (LowHP) Children() []Node { return nil }

func (LowHP) Tick(d *TickContext, _ NodeID) Status {
	threshold := d.Board.FleeHP()
	if threshold <= 0 {
		return Failure
	}
	if d.Board.Health() <= threshold {
		return Success
	}
	return Failure
}

// CanAttack 条件：是否有攻击能力（攻击力 > 0）。
//
// 被动生物（兔/鹿）攻击力为 0，靠这个条件把它们和掠食者分开：
// 它们被打了只能逃，不会进入攻击/追击分支。
type CanAttack struct{ nodeBase }

func (CanAttack) Children() []Node { return nil }

func (CanAttack) Tick(d *TickContext, _ NodeID) Status {
	if d.Board.AttackDamage() > 0 {
		return Success
	}
	return Failure
}

// InAttackRange 条件：目标是否在攻击距离内。
type InAttackRange struct{ nodeBase }

func (InAttackRange) Children() []Node { return nil }

func (InAttackRange) Tick(d *TickContext, _ NodeID) Status {
	if d.Board.Target() == 0 || !d.Board.InAttackRange() {
		return Failure
	}
	return Success
}

// OutOfRoam 条件：是否已越出游荡半径（需要回防出生点）。
type OutOfRoam struct{ nodeBase }

func (OutOfRoam) Children() []Node { return nil }

func (OutOfRoam) Tick(d *TickContext, _ NodeID) Status {
	r := d.Board.RoamRadius()
	if r <= 0 {
		return Failure
	}
	if d.Env.HomeDistance() > r {
		return Success
	}
	return Failure
}

// --- 动作节点 ---

// AttackAction 动作：发起一次攻击（受 Cooldown 装饰器节流）。
//
// 返回：能发起 → Success；冷却/动作占用中 → Running（下 tick 再试）。
// 注意它**只提交控制意图**，真正的伤害结算在 ActionSystem 的 commit 阶段。
type AttackAction struct{ nodeBase }

func (AttackAction) Children() []Node { return nil }

func (AttackAction) Tick(d *TickContext, _ NodeID) Status {
	target := d.Board.Target()
	if target == 0 {
		return Failure
	}
	if !d.Env.AttackReady() {
		return Running
	}
	d.Env.StartAttack(target)
	return Success
}

// ChaseAction 动作：朝目标移动一步（寻路优先）。
//
// 返回：目标有效 → Success（已提交本 tick 的移动意图）；
// 无目标 → Failure。之所以不是 Running：每 tick 都要重新提交移动意图，
// 由 MoveSystem 连续跟随；"到达"不是这个节点的职责（进入攻击分支由
// InAttackRange 条件负责）。
type ChaseAction struct{ nodeBase }

func (ChaseAction) Children() []Node { return nil }

func (ChaseAction) Tick(d *TickContext, _ NodeID) Status {
	target := d.Board.Target()
	if target == 0 {
		return Failure
	}
	d.Env.MoveToward(target)
	return Success
}

// FleeAction 动作：远离目标一步。
type FleeAction struct{ nodeBase }

func (FleeAction) Children() []Node { return nil }

func (FleeAction) Tick(d *TickContext, _ NodeID) Status {
	target := d.Board.Target()
	if target == 0 {
		return Failure
	}
	d.Env.FleeFrom(target)
	return Success
}

// ReturnHomeAction 动作：朝出生点回防一步。
type ReturnHomeAction struct{ nodeBase }

func (ReturnHomeAction) Children() []Node { return nil }

func (ReturnHomeAction) Tick(d *TickContext, _ NodeID) Status {
	d.Env.MoveHome()
	return Success
}

// WanderAction 动作：在出生点附近随机游荡（确定性随机）。
//
// 只在特定 tick 相位换向（沿用现有 idle 的 (now+e)%24==0 节奏），
// 其余 tick 返回 Success 但不提交移动——保持"慢悠悠游荡"的手感，
// 同时不覆盖 MoveSystem 正在走的路径。
type WanderAction struct {
	nodeBase
	// PeriodTicks 换向周期（tick）；<= 0 用缺省 24。
	PeriodTicks int
}

// NewWander 构造游荡动作。
func NewWander(periodTicks int) *WanderAction { return &WanderAction{PeriodTicks: periodTicks} }

func (a *WanderAction) Children() []Node { return nil }

func (a *WanderAction) Tick(d *TickContext, id NodeID) Status {
	period := a.PeriodTicks
	if period <= 0 {
		period = 24
	}
	// 相位包含节点 id 与自身 id，避免所有生物同 tick 一起转向。
	if (d.Board.Now()+int(d.Self)+int(id))%period != 0 {
		return Success
	}
	dx := d.Env.Rand(3) - 1
	dy := d.Env.Rand(3) - 1
	if dx == 0 && dy == 0 {
		return Success
	}
	d.Env.MoveDir(dx, dy)
	return Success
}

// IdleAction 动作：什么都不做（占位/兜底，永远 Success）。
type IdleAction struct{ nodeBase }

func (IdleAction) Children() []Node { return nil }

func (IdleAction) Tick(*TickContext, NodeID) Status { return Success }
