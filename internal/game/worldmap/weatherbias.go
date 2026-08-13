package worldmap

// WeatherBias 区域天气基值（biome 配置，服务端内部；随存档保存）。
// 采样时叠加到全局天气上：沼泽多雾、雪原更冷、沙漠干热等。
type WeatherBias struct {
	Temp float32 `json:"temp_bias"` // 温度偏移
	Fog  float32 `json:"fog_bias"`  // 雾密度偏移
	Rain float32 `json:"rain_bias"` // 降雨偏移
}
