package world

import "time"

// Start 启动世界：WorldActor 收到后开始自驱动 tick（SendRepeat 给自己发 Tick）。
type Start struct{}

// Tick 是一个固定步长的模拟节拍（普通消息，与玩家命令在同一个邮箱里排队）。
type Tick struct{}

// QueryWorldTime 查询当前世界时钟（请求-应答，供外部/网关/测试使用）。
type QueryWorldTime struct{}

// WorldConfig 世界配置。
type WorldConfig struct {
	// TickInterval 模拟步长。默认 100ms（10Hz），生存玩法够用；
	// 动作手感要求高可降到 50ms（20Hz）。ECS tick 开销微秒级，余量充足。
	TickInterval time.Duration
	// HungerRate 每 tick 饥饿消耗：0 = 不消耗（调试默认），<0 = 用默认 1。
	HungerRate int
	// GrowthTicks 可生长实体每多少 tick 长一阶段（默认 20）。
	GrowthTicks int
	// AttackDamage 每次攻击伤害（默认 10）。
	AttackDamage int
	// OfflineRetentionTicks 断线保留时长（tick 数）；默认 3000（10Hz ≈ 5 分钟）。
	OfflineRetentionTicks int
	// CorpseRetentionTicks 尸体保留时长（tick 数）；0 = 永久保留；默认 600（10Hz ≈ 1 分钟）。
	CorpseRetentionTicks int
	// ResourcesPath 资源配置表路径；空表示不 seed 资源实体。
	ResourcesPath string
	// TemplatesPath 资源模板表路径；空表示不加载（采集/掉落/使用无模板）。
	TemplatesPath string
	// RecipesPath 配方表路径；空表示不加载制作配方。
	RecipesPath string
	// StationsPath 工作站配置路径；空表示不 seed 工作站。
	StationsPath string
}
