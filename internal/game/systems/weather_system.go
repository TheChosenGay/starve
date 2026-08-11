package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// WeatherSystem 天气推进（order 20）：每 tick 推进 Weather.Phase
// （季节/温度场/风场随时间演化，采样由 weather 包按相位确定性生成）。
// 温度 → 寒冷/炎热效果的挂钩在天气派生提供者（weather 包 init 注册），
// EffectSystem（order 90）统一结算，保证"覆盖集重算"不会误清除。
type WeatherSystem struct{}

// Update 实现 ECS 系统接口。
func (s *WeatherSystem) Update(w *ecs.World, dt time.Duration) {
	if wr, ok := ecs.TryResource[components.Weather](w); ok {
		wr.Phase++
	}
}
