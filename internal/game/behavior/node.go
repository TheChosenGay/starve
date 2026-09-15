package behavior

// Status 是节点一次 Tick 的返回值（三态）。
type Status uint8

const (
	// Success 表示节点本次目标已达成。
	Success Status = iota
	// Failure 表示节点本次无法达成（且没有"正在做"的进度）。
	Failure
	// Running 表示节点已开始但尚未完成，下 tick 应继续而不是重头开始。
	Running
)

// String 便于日志/测试断言可读。
func (s Status) String() string {
	switch s {
	case Success:
		return "Success"
	case Failure:
		return "Failure"
	case Running:
		return "Running"
	}
	return "Unknown"
}

// Node 是所有行为树节点的统一接口。
//
// Tick 返回本次执行结果；允许有副作用（动作节点写控制意图），但**不许**
// 直接改实体位置/血量——那些交给系统在树执行之后统一仲裁（与项目现有
// "系统产出控制意图、ControlSystem 统一仲裁"的分工一致）。
//
// 节点实现必须是**无状态**的：所有跨 tick 的进度存在黑板的节点运行态里
// （见 NodeState），这样同一棵树定义可以被多个实体共享。
type Node interface {
	// Tick 执行一次。d 是该节点在树中的唯一 id（用于存/取运行态）。
	Tick(d *TickContext, id NodeID) Status
	// Children 返回子节点（叶子返回 nil）。树的遍历与校验依赖它。
	Children() []Node
}

// NodeID 是节点在树内的稳定标识（自增序号，构建时分配）。
//
// 为什么不用指针做 key：运行态要进存档/快照，指针跨进程无意义；
// 而自增 id 只要**构建顺序固定**就是稳定的（同一份定义 → 同一批 id）。
//
// 所有内置节点都内嵌 nodeBase，由 Tree 在构建时统一分配 id 并写回节点。
// 节点因此**不能**在多个树之间共享实例——每棵树各自构建自己的节点。
type NodeID uint32

// nodeBase 是所有内置节点的公共部分：持有自己的 NodeID。
//
// 内嵌它（而不是让每个节点各自实现 ID()）是为了让 idOf 用一次接口断言
// 就能取到 id，同时保证"忘记内嵌 = 编译期就该发现"的问题在测试里暴露。
type nodeBase struct {
	id NodeID
}

// ID 返回节点的树内标识（未分配时为 0，见 Tree.assignIDs）。
func (b *nodeBase) ID() NodeID { return b.id }

// identify 由 Tree.assignIDs 调用，给节点写回分配好的 id。
func (b *nodeBase) identify(id NodeID) { b.id = id }

// identifier 是"能被分配 id 的节点"的接口（所有内置节点通过内嵌 nodeBase 满足）。
type identifier interface {
	identify(id NodeID)
}

// idOf 取节点的 NodeID；没有内嵌 nodeBase 的自定义节点返回 0。
//
// 返回 0 意味着"该节点不参与运行态存取"——对无状态节点（条件、纯动作）
// 完全没问题；需要 Running 记忆或冷却计时的节点必须内嵌 nodeBase。
func idOf(n Node) NodeID {
	if b, ok := n.(interface{ ID() NodeID }); ok {
		return b.ID()
	}
	return 0
}

// TickContext 是一次树遍历的上下文：黑板 + 环境 + 运行态存储。
//
// 它是整个遍历过程中唯一的可变载体，节点通过它读写一切。
type TickContext struct {
	// Board 是决策数据区（目标、血量、距离…）。
	Board Blackboard
	// Env 提供对世界的只读查询与确定性随机。
	Env Env
	// State 存取节点的跨 tick 运行态（Running 游标、冷却计时）。
	State NodeStateStore
	// self 是本次运行树的实体 id（透传给 Env，便于日志/调试）。
	Self uint64
}

// NewTickContext 组装一次遍历的上下文。
func NewTickContext(board Blackboard, env Env, state NodeStateStore, self uint64) *TickContext {
	return &TickContext{Board: board, Env: env, State: state, Self: self}
}

// Child 取指定下标的子节点（越界返回 nil，避免调用方到处判长度）。
func Child(n Node, i int) Node {
	kids := n.Children()
	if i < 0 || i >= len(kids) {
		return nil
	}
	return kids[i]
}

// tickChild 执行第 i 个子节点；子节点不存在时返回 Failure。
func tickChild(d *TickContext, n Node, i int) Status {
	c := Child(n, i)
	if c == nil {
		return Failure
	}
	return tickNode(d, c)
}

// tickNode 执行一个子节点，并把它的 NodeID 交给它（供运行态存取）。
//
// 节点的 id 由**父节点在构建时登记进 idTable**（见 Tree.assignIDs），
// 这里通过 idOf 查表得到——节点本身不存 id，保持"定义可共享、运行态可变"。
func tickNode(d *TickContext, n Node) Status {
	return n.Tick(d, idOf(n))
}
