package components

// Weather 是世界级天气资源（Resource，非实体组件）：时间相位 + 采样配置。
// 相位由 WeatherSystem 每 tick 推进；季节由相位推导（确定性，随存档自动恢复）。
// 冷/热阈值与伤害在这里（构造时由 world 从配置写入），天气系统按需采样。
type Weather struct {
	Phase      int64   // 时间相位（世界 tick）
	Seed       uint64  // 噪声种子（= 地图种子；确定性）
	YearTicks  int64   // 一年 tick 数（默认 9600，每季 2400）
	ColdAt     float32 // 温度 ≤ 此值挂寒冷（默认关闭用极低值）
	ColdDamage int
	HeatAt     float32 // 温度 ≥ 此值挂炎热（默认关闭用极高值）
	HeatDamage int
}

// SeasonOf 由相位推导季节（单一事实来源；weather 包采样也用这个）。
func SeasonOf(phase, yearTicks int64) Season {
	if yearTicks <= 0 {
		yearTicks = 9600
	}
	seg := yearTicks / 4
	switch q := phase % yearTicks; {
	case q < seg:
		return SeasonSpring
	case q < seg*2:
		return SeasonSummer
	case q < seg*3:
		return SeasonAutumn
	default:
		return SeasonWinter
	}
}

// Season 由相位推导当前季节（确定性）。
func (w *Weather) Season() Season { return SeasonOf(w.Phase, w.YearTicks) }
