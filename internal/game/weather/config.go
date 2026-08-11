package weather

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config 天气参数（configs/weather.json）：季节长度 + 冷/热阈值与伤害。
// 配置驱动：调阈值/伤害只改配置，不改代码。
type Config struct {
	YearTicks  int64   `json:"year_ticks"`
	ColdAt     float32 `json:"cold_at"`     // 温度 ≤ 此值挂寒冷
	ColdDamage int     `json:"cold_damage"` // 每 tick 冻伤量
	HeatAt     float32 `json:"heat_at"`     // 温度 ≥ 此值挂炎热
	HeatDamage int     `json:"heat_damage"` // 每 tick 中暑量
}

// DefaultConfig 默认：气候伤害关闭（阈值取极值），需要时在配置里打开。
func DefaultConfig() *Config {
	return &Config{YearTicks: 9600, ColdAt: -100, ColdDamage: 1, HeatAt: 100, HeatDamage: 1}
}

// LoadConfig 读取 weather.json，缺字段用默认值。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := DefaultConfig()
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("weather: %w", err)
	}
	if cfg.YearTicks <= 0 {
		cfg.YearTicks = 9600
	}
	return cfg, nil
}
