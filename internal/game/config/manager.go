package config

import (
	"os"
	"strconv"
	"time"
)

// ConfigKind 配置类别（ConfigManager 按类别管理路径/加载）。
type ConfigKind string

// 全部配置类别。
const (
	ConfigResources ConfigKind = "resources"
	ConfigTemplates ConfigKind = "templates"
	ConfigRecipes   ConfigKind = "recipes"
	ConfigStations  ConfigKind = "stations"
	ConfigMap       ConfigKind = "map"
	ConfigWeather   ConfigKind = "weather"
	ConfigBiomes    ConfigKind = "biomes"
)

// ConfigManager 集中管理全部世界配置：路径（含环境变量默认）+ 加载 + 运行时参数。
// 环境变量由内部处理（GATE_*），外部只需：NewConfigManagerFromEnv → SetPath 调整 →
// Load() 拿 GameConfig、WorldConfig() 拿运行参数，交给 NewWorldActorWithConfig。
type ConfigManager struct {
	paths map[ConfigKind]string

	TickInterval      time.Duration // 默认 50ms（20Hz）
	HungerRate        int
	MoveInterval      int // 默认 1（tick/格）
	OfflineSeconds    int // 默认 300
	CorpseSeconds     int // 默认 60
	InventorySlots    int // 默认 20
	WeatherFrameTicks int
	MapSeed           uint64 // 默认 42
}

// NewConfigManager 空管理器（测试/程序化用，路径为空 = 不加载该类）。
func NewConfigManager() *ConfigManager {
	return &ConfigManager{paths: map[ConfigKind]string{}}
}

// NewConfigManagerFromEnv 从 GATE_* 环境变量构造（默认值内置，环境变量可覆盖）。
func NewConfigManagerFromEnv() *ConfigManager {
	m := NewConfigManager()
	m.SetPath(ConfigResources, EnvOr("GATE_RESOURCES", "configs/resources.json"))
	m.SetPath(ConfigTemplates, EnvOr("GATE_TEMPLATES", "configs/resource_templates.json"))
	m.SetPath(ConfigRecipes, EnvOr("GATE_RECIPES", "configs/crafting.json"))
	m.SetPath(ConfigStations, EnvOr("GATE_STATIONS", "configs/stations.json"))
	m.SetPath(ConfigMap, EnvOr("GATE_MAP", "configs/map.json"))
	m.SetPath(ConfigWeather, EnvOr("GATE_WEATHER", "configs/weather.json"))
	m.SetPath(ConfigBiomes, EnvOr("GATE_BIOMES", "configs/biomes.json"))
	m.TickInterval = time.Duration(EnvOrInt("GATE_TICK_MS", 50)) * time.Millisecond
	m.HungerRate = EnvOrInt("GATE_HUNGER_RATE", 0)
	m.MoveInterval = EnvOrInt("GATE_MOVE_INTERVAL", 1)
	m.OfflineSeconds = EnvOrInt("GATE_OFFLINE_SECONDS", 300)
	m.CorpseSeconds = EnvOrInt("GATE_CORPSE_SECONDS", 60)
	m.InventorySlots = EnvOrInt("GATE_INVENTORY_SLOTS", 20)
	m.MapSeed = EnvOrUint64("GATE_MAP_SEED", 42)
	return m
}

// SetPath 设置某类配置的路径（空 = 禁用该类加载）。
func (m *ConfigManager) SetPath(kind ConfigKind, path string) { m.paths[kind] = path }

// Path 返回某类配置路径（未设置 = ""）。
func (m *ConfigManager) Path(kind ConfigKind) string { return m.paths[kind] }

// Kinds 枚举全部配置类别（固定顺序，确定性）。
func (m *ConfigManager) Kinds() []ConfigKind {
	return []ConfigKind{ConfigResources, ConfigTemplates, ConfigRecipes, ConfigStations, ConfigMap, ConfigWeather, ConfigBiomes}
}

// WorldConfig 把运行时参数 + 路径组装成 WorldConfig（供 NewWorldActor / NewWorldActorWithConfig）。
func (m *ConfigManager) WorldConfig() WorldConfig {
	tickMS := int(m.TickInterval / time.Millisecond)
	if tickMS <= 0 {
		tickMS = 50
	}
	return WorldConfig{
		TickInterval:          m.TickInterval,
		HungerRate:            m.HungerRate,
		MoveInterval:          m.MoveInterval,
		OfflineRetentionTicks: m.OfflineSeconds * 1000 / tickMS,
		CorpseRetentionTicks:  m.CorpseSeconds * 1000 / tickMS,
		InventorySlots:        m.InventorySlots,
		WeatherFrameTicks:     m.WeatherFrameTicks,
		MapSeed:               m.MapSeed,
		ResourcesPath:         m.paths[ConfigResources],
		TemplatesPath:         m.paths[ConfigTemplates],
		RecipesPath:           m.paths[ConfigRecipes],
		StationsPath:          m.paths[ConfigStations],
		MapPath:               m.paths[ConfigMap],
		WeatherPath:           m.paths[ConfigWeather],
		BiomesPath:            m.paths[ConfigBiomes],
	}
}

// Load 按当前路径加载全部配置（= LoadGameConfig(WorldConfig())）。
func (m *ConfigManager) Load() (*GameConfig, error) {
	return LoadGameConfig(m.WorldConfig())
}

// EnvOr 读取环境变量，未设置用默认值。
func EnvOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// EnvOrInt 读取环境变量为整数，非法/未设置用默认值。
func EnvOrInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// EnvOrUint64 读取环境变量为无符号整数，非法/未设置用默认值。
func EnvOrUint64(key string, def uint64) uint64 {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
