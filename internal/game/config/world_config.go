package config

import "time"

// WorldConfig 世界运行参数（ConfigManager 组装；NewWorldActor 消费）。
type WorldConfig struct {
	// TickInterval 模拟步长。默认 50ms（20Hz），动作手感与生存玩法兼顾；
	// 需要更省时也可调回 100ms（10Hz）。ECS tick 开销微秒级，余量充足。
	TickInterval time.Duration
	// HungerRate 每 tick 饥饿消耗：0 = 不消耗（调试默认），<0 = 用默认 1。
	HungerRate int
	// GrowthTicks 可生长实体每多少 tick 长一阶段（默认 20）。
	GrowthTicks int
	// AttackDamage 每次攻击伤害（默认 10）。
	AttackDamage int
	// MoveInterval 玩家基础步进间隔（tick/格，默认 2 = 每 2 tick 走一格，20Hz 下 10 格/秒）。
	MoveInterval int
	// OfflineRetentionTicks 断线保留时长（tick 数）；默认 6000（20Hz ≈ 5 分钟）。
	OfflineRetentionTicks int
	// CorpseRetentionTicks 尸体保留时长（tick 数）；0 = 永久保留；默认 1200（20Hz ≈ 1 分钟）。
	CorpseRetentionTicks int
	// InventorySlots 背包格数；默认 20。
	InventorySlots int
	// ResourcesPath 资源配置表路径；空表示不 seed 资源实体。
	ResourcesPath string
	// TemplatesPath 资源模板表路径；空表示不加载（采集/掉落/使用无模板）。
	TemplatesPath string
	// RecipesPath 配方表路径；空表示不加载制作配方。
	RecipesPath string
	// StationsPath 工作站配置路径；空表示不 seed 工作站。
	StationsPath string
	// WeatherPath 天气参数配置路径（weather.json）；空表示用默认（气候伤害关闭）。
	WeatherPath string
	// BiomesPath 生物群系配置路径（biomes.json）；空表示不启用区域布局。
	BiomesPath string
	// WeatherFrameTicks 天气帧推送间隔（tick）；0 = 用默认 20（20Hz 下 1Hz）；负值 = 关闭推送。
	WeatherFrameTicks int
	// MapPath 地图规格路径（map.json）；空表示回退到 ResourcesPath/StationsPath 手摆。
	MapPath string
	// MapSeed 地图生成种子（确定性）；默认 42。
	MapSeed uint64
}
