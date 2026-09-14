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
	// MoveSpeed 玩家移动速度（格/秒，默认 10）。
	MoveSpeed float64
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
	// CreaturesPath 生物模板配置路径（creatures.json）；空表示不生成生物。
	CreaturesPath string
	// BuildingsPath 建筑模板配置路径（buildings.json）；空表示建造回退 1×1。
	BuildingsPath string
	// DebugAOI 调试开关：AOI.Visible 进快照并随变更推送（客户端调试感知范围）。
	DebugAOI bool
	// DebugCollision 调试开关：实体简化碰撞体（DebugShape）进快照，客户端渲染出来，
	// 用来核对"碰撞体是不是刚好包住渲染模型"（GATE_DEBUG_COLLISION=1）。
	DebugCollision bool
	// AOIInterval AOI 感知刷新间隔（tick）；0 = 默认 4（20Hz 下 ≈ 4Hz）。
	AOIInterval int
	// ViewRadius 相机半径下限（拉近，切比雪夫格数）。0 = 默认 24；负数 = 不裁剪（全图下发）。
	ViewRadius int
	// ViewRadiusMax 相机半径上限（拉远 / 最大加载范围）。0 = 等于 ViewRadius；负数 = 不裁剪。
	// 服务端下发半径 = ViewRadiusMax + ViewPreload。
	ViewRadiusMax int
	// ViewPreload 相对 ViewRadiusMax 多下发的格数（屏幕外预加载）。0 = 默认 8；负数 = 不预加载。
	ViewPreload int
	// WeatherFrameTicks 天气帧推送间隔（tick）；0 = 用默认 20（20Hz 下 1Hz）；负值 = 关闭推送。
	WeatherFrameTicks int
	// MapPath 地图规格路径（map.json）；空表示回退到 ResourcesPath/StationsPath 手摆。
	MapPath string
	// MapSeed 地图生成种子（确定性）；默认 42。
	MapSeed uint64
}

// DefaultViewRadius 相机半径下限默认值（与 GATE_VIEW_RADIUS 默认值一致）。
const DefaultViewRadius = 24

// DefaultViewRadiusMax 拉远上限默认值（与 GATE_VIEW_RADIUS_MAX 默认值一致）。
const DefaultViewRadiusMax = 32

// DefaultViewPreload 相对相机上限多下发的格数（10 格/秒下约 0.8 秒行程）。
const DefaultViewPreload = 8

// NormalizeViewRadius 契约/模拟共用：0 → 默认 24；负数 → -1（不裁剪）。
func NormalizeViewRadius(r int) int {
	if r < 0 {
		return -1
	}
	if r == 0 {
		return DefaultViewRadius
	}
	return r
}

// NormalizeViewRadiusMax：负数 → -1；0 → 等于已规范化的下限；小于下限则抬到下限。
func NormalizeViewRadiusMax(viewRadius, viewRadiusMax int) int {
	r := NormalizeViewRadius(viewRadius)
	if r < 0 || viewRadiusMax < 0 {
		return -1
	}
	if viewRadiusMax == 0 {
		return r
	}
	if viewRadiusMax < r {
		return r
	}
	return viewRadiusMax
}

// NormalizeViewPreload：0 → 默认 8；负数 → 0（关闭预加载）。
func NormalizeViewPreload(p int) int {
	if p < 0 {
		return 0
	}
	if p == 0 {
		return DefaultViewPreload
	}
	return p
}

// InterestRadius 服务端下发半径 = 相机上限 + 预加载；不裁剪时仍为 -1。
func InterestRadius(viewRadius, viewRadiusMax, viewPreload int) int {
	m := NormalizeViewRadiusMax(viewRadius, viewRadiusMax)
	if m < 0 {
		return -1
	}
	return m + NormalizeViewPreload(viewPreload)
}
