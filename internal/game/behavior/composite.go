package behavior

// Sequence 顺序节点：从左到右依次执行子节点。
//
//	任一子节点 Failure → 立即返回 Failure（短路，后面的不执行）
//	任一子节点 Running → 记住位置并返回 Running（下 tick 从该子节点续跑）
//	全部 Success       → 返回 Success
//
// 典型用途："先走到目标 → 再攻击"（走不到就不攻击）。
type Sequence struct {
	nodeBase
	children []Node
}

// NewSequence 构造顺序节点。
func NewSequence(children ...Node) *Sequence {
	return &Sequence{children: children}
}

// Children 返回子节点。
func (s *Sequence) Children() []Node { return s.children }

// Tick 实现 Node。
func (s *Sequence) Tick(d *TickContext, id NodeID) Status {
	start := 0
	// 续跑：上次从第 idx 个子节点开始 Running，本 tick 直接从它继续，
	// 不再重跑前面已成功的兄弟（否则"走过去"会被反复执行）。
	if idx, ok := d.State.RunningChildOf(id); ok {
		start = int(idx)
	}
	for i := start; i < len(s.children); i++ {
		switch tickNode(d, s.children[i]) {
		case Failure:
			d.State.ClearRunningChildOf(id)
			return Failure
		case Running:
			d.State.SetRunningChildOf(id, uint8(i))
			return Running
		}
	}
	d.State.ClearRunningChildOf(id)
	return Success
}

// Selector 选择节点：从左到右依次尝试子节点，直到有一个不失败。
//
//	任一子节点 Success → 立即返回 Success（短路）
//	任一子节点 Running → 记住位置并返回 Running
//	全部 Failure       → 返回 Failure
//
// 这是**替代硬编码 switch 的核心节点**："能打就打，不能打就追，都
// 不行就游荡"——按优先级从上往下试，第一个能做的赢。
type Selector struct {
	nodeBase
	children []Node
}

// NewSelector 构造选择节点。
func NewSelector(children ...Node) *Selector {
	return &Selector{children: children}
}

// Children 返回子节点。
func (s *Selector) Children() []Node { return s.children }

// Tick 实现 Node。
func (s *Selector) Tick(d *TickContext, id NodeID) Status {
	start := 0
	if idx, ok := d.State.RunningChildOf(id); ok {
		start = int(idx)
	}
	for i := start; i < len(s.children); i++ {
		switch tickNode(d, s.children[i]) {
		case Success:
			d.State.ClearRunningChildOf(id)
			return Success
		case Running:
			d.State.SetRunningChildOf(id, uint8(i))
			return Running
		}
	}
	d.State.ClearRunningChildOf(id)
	return Failure
}

// Inverter 装饰节点：把子节点的 Success ↔ Failure 取反，Running 原样透传。
//
// 用途：把条件节点反过来用（"看不见目标" = Inverter(看见目标)）。
type Inverter struct {
	nodeBase
	child Node
}

// NewInverter 构造取反装饰器。
func NewInverter(child Node) *Inverter { return &Inverter{child: child} }

// Children 返回子节点。
func (n *Inverter) Children() []Node { return []Node{n.child} }

// Tick 实现 Node。
func (n *Inverter) Tick(d *TickContext, _ NodeID) Status {
	switch tickNode(d, n.child) {
	case Success:
		return Failure
	case Failure:
		return Success
	}
	return Running
}

// Succeeder 装饰节点：无论子节点结果如何都返回 Success（Running 仍透传）。
//
// 用途：让"尽力而为"的可选行为不打断 Sequence（例如"顺手游荡一下"
// 不该因为游荡失败而让整个分支失败）。
type Succeeder struct {
	nodeBase
	child Node
}

// NewSucceeder 构造恒成功装饰器。
func NewSucceeder(child Node) *Succeeder { return &Succeeder{child: child} }

// Children 返回子节点。
func (n *Succeeder) Children() []Node { return []Node{n.child} }

// Tick 实现 Node。
func (n *Succeeder) Tick(d *TickContext, _ NodeID) Status {
	switch tickNode(d, n.child) {
	case Running:
		return Running
	}
	return Success
}

// Cooldown 装饰节点：限制子节点在 N tick 内最多执行一次。
//
// 语义：处于冷却中 → 返回 Failure（不执行子节点，让外层 Selector 试别人）；
// 冷却是"剩余 tick"，每 tick 递减，减到 0 后放行一次，执行后重新装满。
//
// 用途：攻击冷却——AI 的 Cooldown 字段由它统一管理，不再手写计数。
type Cooldown struct {
	nodeBase
	child Node
	ticks int
}

// NewCooldown 构造冷却装饰器（ticks <= 0 时退化为直接透传子节点）。
func NewCooldown(ticks int, child Node) *Cooldown {
	return &Cooldown{child: child, ticks: ticks}
}

// Children 返回子节点。
func (n *Cooldown) Children() []Node { return []Node{n.child} }

// Tick 实现 Node。
func (n *Cooldown) Tick(d *TickContext, id NodeID) Status {
	if n.ticks <= 0 {
		return tickNode(d, n.child)
	}
	left := d.State.IntOf(id)
	if left > 0 {
		d.State.SetIntOf(id, left-1)
		return Failure
	}
	switch st := tickNode(d, n.child); st {
	case Success, Running:
		// Running 也算"已经用掉这次机会"：重新装填，避免同一动作被反复放行。
		d.State.SetIntOf(id, n.ticks)
		return st
	default:
		return Failure
	}
}
