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
	// CorpseRetentionTicks 尸体保留时长（tick 数）；0 = 永久保留。
	// 由 GATE_CORPSE_SECONDS 换算（缺省 60 秒），仅用作 NpcCorpseRetentionTicks
	// 的兜底来源（未显式设置时）。
	CorpseRetentionTicks int
	// NpcCorpseRetentionTicks **NPC** 尸体保留时长（tick 数）；0 = 不回收。
	//
	// 与玩家彻底分开：玩家尸体永久保留（重连复用实体，靠 Offline TTL 回收），
	// NPC 尸体只需留一个"看到尸体"的短窗口。
	// 缺省 200 tick ≈ 10 秒（原实现误用 60 秒，且把玩家排除在回收之外，正好写反）。
	NpcCorpseRetentionTicks int
	// InventorySlots 背包格数；默认 20。
	InventorySlots int
	// ThrowStrength 玩家裸手投掷力量（决定最大投掷距离，0 = 用缺省）。
	//
	// 放在世界配置而不是物品模板：力量是**投掷者**的属性（不同玩家/生物可不同），
	// 质量才是物品的属性。两者相乘决定距离。
	ThrowStrength int
	// StartingBombs 出生时赠送的炸弹数量（0 = 不给）。
	//
	// 用"送几个炸弹"而不是"造一个世界物品"：炸弹是可投掷物，需要玩家
	// 拿在手里（投掷校验要求被投物在投掷者身边）。送进背包 + 投掷时
	// 从背包取一个放到手上，才是完整的物品流转。
	StartingBombs int
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
