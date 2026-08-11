package weather

import (
	"math"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// WeatherQuery 查询参数：一个位置的具体天气由这些决定。
// 季节由世界时钟推导（components.SeasonOf），查询时传入。
type WeatherQuery struct {
	X, Y     int               // 世界坐标
	Height   int               // 该处高度（角高度）
	TileType game.TerrainType  // 地形类型
	Season   components.Season // 季节
	Tick     int64             // 世界 tick（时间维度，天气随时间演化）
}

// WeatherSample 采样结果：该位置的具体天气。
type WeatherSample struct {
	Temperature float32 // 温度（摄氏度语义）
	Fog         float32 // 雾密度 0..1
	Rain        float32 // 降雨强度 0..1
	WindDirX    float32 // 风向（单位向量，便于和风扇向量叠加）
	WindDirY    float32
	WindSpeed   float32 // 风速
	Light       float32 // 云层遮光系数 0..1（1 = 无云）
}

// Sampler 确定性天气采样器：seed 化 value noise + FBM，纯函数无副作用。
// 同一 seed + 查询 → 同一结果（确定性/重放一致）。
type Sampler struct {
	seed      uint64
	scale     float32 // 空间缩放（格 → 噪声坐标）
	timeScale float32 // tick → 噪声时间坐标
}

// NewSampler 用世界种子构造采样器。
func NewSampler(seed uint64) *Sampler {
	return &Sampler{seed: seed, scale: 0.02, timeScale: 0.0002}
}

// WeatherAt 采样一个位置的天气：基底噪声（温度/雨/雾/风，随时间演化）
// + 季节修正 + 高度直减率 + 地形湿冷/风修正。纯函数。
func (s *Sampler) WeatherAt(q WeatherQuery) WeatherSample {
	x := float32(q.X) * s.scale
	y := float32(q.Y) * s.scale
	t := float32(q.Tick) * s.timeScale

	out := WeatherSample{}
	out.Temperature = lerp(-10, 35, s.fbm(x, y, t, 4)) + seasonTemp(q.Season)
	out.Rain = clamp01(s.fbm(x+31, y+17, t, 3)*1.2 - 0.25 + seasonRain(q.Season))
	out.Fog = clamp01(s.fbm(x+7, y+53, t*0.8, 3)*0.8 + 0.1 + out.Rain*0.4)
	ang := s.fbm(x+89, y+113, t, 2) * float32(math.Pi) * 2
	out.WindDirX = float32(math.Cos(float64(ang)))
	out.WindDirY = float32(math.Sin(float64(ang)))
	out.WindSpeed = 0.5 + s.fbm(x+137, y+151, t, 2)*4

	// 高度直减率 + 地形修正
	out.Temperature -= float32(q.Height) * 1.5
	switch q.TileType {
	case game.TerrainType_TERRAIN_TYPE_WATER:
		out.Temperature -= 3
		out.Fog += 0.15
		out.Rain += 0.05
	case game.TerrainType_TERRAIN_TYPE_SNOW:
		out.Temperature -= 8
		out.Fog += 0.1
	case game.TerrainType_TERRAIN_TYPE_ROCK:
		out.WindSpeed += 1.5
		out.Fog -= 0.05
	case game.TerrainType_TERRAIN_TYPE_SAND:
		out.Temperature += 2
		out.Fog -= 0.1
	}

	out.Temperature = clamp(out.Temperature, -30, 45)
	out.Fog = clamp01(out.Fog)
	out.Rain = clamp01(out.Rain)
	out.WindSpeed = clamp(out.WindSpeed, 0, 15)
	out.Light = clamp01(1 - out.Rain*0.8 - out.Fog*0.3)
	return out
}

// SampleAt 世界层采样：纯函数结果 + 局部修正（风扇/火堆，实体 ID 升序确定性）。
// 无天气资源时返回中性样本（避免调用方特判）。
func SampleAt(w *ecs.World, q WeatherQuery) WeatherSample {
	wr, ok := ecs.TryResource[components.Weather](w)
	if !ok {
		return WeatherSample{Temperature: 20, WindSpeed: 1, Light: 1}
	}
	out := NewSampler(wr.Seed).WeatherAt(q)
	applyModifiers(w, q, &out)
	return out
}

func seasonTemp(s components.Season) float32 {
	switch s {
	case components.SeasonSummer:
		return 8
	case components.SeasonAutumn:
		return 2
	case components.SeasonWinter:
		return -12
	}
	return 0
}

func seasonRain(s components.Season) float32 {
	switch s {
	case components.SeasonSpring:
		return 0.05
	case components.SeasonSummer:
		return 0.15
	case components.SeasonAutumn:
		return 0.1
	}
	return 0
}

func lerp(a, b, f float32) float32 { return a + (b-a)*f }

func clamp01(v float32) float32 { return clamp(v, 0, 1) }

func clamp(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
