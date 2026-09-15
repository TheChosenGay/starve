package behavior

// Blackboard 是行为树决策所需的**数据区**：条件节点读它，动作节点写它。
//
// 为什么用接口而不是直接引用 ECS 组件：行为树因此可以脱离 ECS 单测
// （见 behavior 包测试），桥接层（components 包）负责把 AI/Creature 组件
// 的字段映射到这里。这也让"AI 决策数据"的边界显式化——新增决策输入
// 就是给这个接口加一个方法，而不是到处 ecs.Get。
//
// 命名用 Get/Set 前缀而非字段，是为了让桥接实现能直接转发到组件字段。
type Blackboard interface {
	// Target 当前锁定的目标实体（0 = 无目标）。
	Target() uint64
	SetTarget(e uint64)

	// Self 本实体 id（用于排除自己、日志）。
	Self() uint64

	// Health/HealthMax 自身血量，供"低血逃跑"类条件判断。
	Health() int
	HealthMax() int

	// FleeHP 逃跑阈值（血 <= 此值应逃跑；0 = 永不逃跑）。
	FleeHP() int

	// AttackDamage 当前攻击力（<= 0 = 无法攻击，被动生物）。
	AttackDamage() int
	// AttackRange 当前攻击距离（格）。
	AttackRange() int
	// AttackCooldownTicks 攻击冷却间隔（tick）。
	AttackCooldownTicks() int

	// InAttackRange 目标是否已在攻击距离内（无目标 = false）。
	InAttackRange() bool

	// CanSee 目标是否在感知内（AOI.Visible）。
	CanSee(e uint64) bool

	// RoamRadius 游荡半径（围绕出生点；<= 0 = 不游荡）。
	RoamRadius() int
	// HomeX/HomeY 游荡锚点（出生点）。
	HomeX() int
	HomeY() int

	// Now 当前世界 tick（确定性时间轴，DayCycle.Phase）。
	Now() int
}

// Env 是行为树对世界的**只读查询**入口 + 确定性随机源。
//
// 动作节点产生副作用的正道是"写控制意图"（见 ActionEnv），而不是直接改世界；
// Env 只提供查询（能不能走、路怎么走）和随机，保证行为树本身是纯决策层。
type Env interface {
	// Rand 返回 [0, n) 的确定性随机数（n <= 0 时返回 0）。
	//
	// 由调用方按"世界种子 + 实体 id + tick"派生子种子，保证同种子同结果
	// （与 AISystem 现有的 splitmix 约定一致，不要用 math/rand 全局源）。
	Rand(n int) int

	// MoveDir 提交一次方向移动意图（dx/dy ∈ {-1,0,1}；0,0 = 不动）。
	MoveDir(dx, dy int)
	// MovePath 提交一条路径（连续跟随）；空路径 = 不做。
	MovePath(path []MoveStep)
	// MoveToward 朝目标实体移动一步（寻路优先，不可达时退化为不移动）。
	MoveToward(target uint64)
	// FleeFrom 远离指定实体一步（寻路优先）。
	FleeFrom(target uint64)
	// MoveHome 朝出生点回防一步（超出游荡半径时用）。
	MoveHome()
	// StartAttack 发起一次攻击动作（目标 + 冷却）。
	StartAttack(target uint64)
	// AttackReady 攻击冷却是否已结束（false = 本 tick 应继续等冷却）。
	AttackReady() bool

	// HomeDistance 距出生点的曼哈顿距离（格）。
	HomeDistance() int
}

// MoveStep 是一个路径点方向（与 components.MoveDir 同形，避免反向依赖）。
type MoveStep struct{ DX, DY int }

// NodeStateStore 存取节点的跨 tick 运行态。
//
// 两种运行态：
//   - Running 游标：记录"上次哪个子节点在 Running"，下 tick 直接续跑；
//   - 计数器：冷却剩余 tick 等（用 Int 系列）。
//
// 实现方（组件）负责把它编码进快照/存档，因此这里只用可序列化的
// uint32/uint8 值，不暴露指针。
//
// 方法名统一带 Of 后缀（RunningChildOf/SetRunningChildOf…）：实现方是
// BehaviorTree 组件，其**字段**正好也叫 RunningChild/Counters，同名的
// 方法会与字段冲突，加后缀后既无歧义又能让结构体直接实现本接口。
type NodeStateStore interface {
	// RunningChildOf 取节点 id 上次记忆的 Running 子节点下标。
	// 返回 (0, false) 表示没有记忆（应从头开始）。
	RunningChildOf(id NodeID) (uint8, bool)
	// SetRunningChildOf 记住"正在 Running 的是第几个子节点"。
	SetRunningChildOf(id NodeID, idx uint8)
	// ClearRunningChildOf 清除记忆（子树结束/整棵树重启时）。
	ClearRunningChildOf(id NodeID)

	// IntOf 取节点 id 的计数器（不存在返回 0）。
	IntOf(id NodeID) int
	// SetIntOf 写计数器。
	SetIntOf(id NodeID, v int)
	// ClearIntOf 删除计数器。
	ClearIntOf(id NodeID)
}

// ActionEnv 是 Env 的扩展：动作节点需要"读黑板 + 写意图"之外的能力时用它。
//
// 目前 Env 已覆盖所需能力，这个接口留作扩展点（例如后续行为树要触发
// 群体仇恨广播时，加 ShoutForHelp() 而不破坏既有实现）。
type ActionEnv interface {
	Env
}
