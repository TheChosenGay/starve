// Package behavior 是行为树（Behavior Tree）运行时。
//
// 定位：把 AI 的"做什么"从 AISystem 的硬编码 switch 里拆出来，变成
// **可组合的节点树**。AISystem 只负责"每 tick 驱动一次树"，决策逻辑
// 全部由树表达。
//
// # 核心概念
//
// Tree 每 tick 从根节点开始执行（Tick），自顶向下传播，节点返回三态：
//
//	Success  做成了
//	Failure  做不了
//	Running  正在做、还没完（下 tick 继续，不重头开始）
//
// Running 是行为树区别于普通决策表的关键：它让"挥刀 8 tick 前摇"这类
// **跨 tick 动作**能自然表达——动作自己记住进度，树只负责"还在做"。
//
// # 节点分类
//
//	组合（Composite）  Sequence / Selector：决定子节点的执行顺序
//	装饰（Decorator）  Cooldown / Inverter / Succeeder：包装单个子节点，改其行为
//	条件（Condition）  只读黑板，不产生副作用，返回 Success/Failure
//	动作（Action）     产生实际行为（写控制意图），可返回 Running
//
// # 与 ECS 的分工
//
// 本包**不感知 ECS 组件布局**，只认 Blackboard 接口（读写 AI 决策所需的
// 数据）与 Env（访问世界做查询）。这样行为树可以独立单测，不需要造整个世界。
// 桥接 ECS 的实现见 internal/game/components 的 BehaviorTree 组件。
//
// # 确定性约束（与全项目一致）
//
//   - 节点不持有可变全局状态；每个实体的运行状态存在自己的运行态里；
//   - 遍历子节点一律按切片顺序（定义顺序），不做 map 遍历；
//   - 需要随机时用 Env.Rand（由调用方提供确定性种子），不用 math/rand 全局源。
package behavior
