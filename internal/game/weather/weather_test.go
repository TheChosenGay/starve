package weather

import (
	"testing"

	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

func sampleQ(x, y int, tick int64) WeatherQuery {
	return WeatherQuery{X: x, Y: y, Tick: tick, Season: components.SeasonSpring, TileType: game.TerrainType_TERRAIN_TYPE_GRASS}
}

// 确定性：同 seed + 同查询 → 完全一致；不同位置/时间 → 不同（空间/时间演化）。
func TestWeatherAtDeterministic(t *testing.T) {
	s := NewSampler(42)
	a := s.WeatherAt(sampleQ(1, 2, 100))
	b := s.WeatherAt(sampleQ(1, 2, 100))
	if a != b {
		t.Fatalf("同查询结果不一致: %+v vs %+v", a, b)
	}
	c := s.WeatherAt(sampleQ(2, 2, 100))
	if a == c {
		t.Fatal("不同位置结果不应相同")
	}
	d := s.WeatherAt(sampleQ(1, 2, 101))
	if a == d {
		t.Fatal("不同 tick 结果不应相同（时间演化）")
	}
}

// 季节推导：一年 9600 tick，每季 2400。
func TestSeasonOf(t *testing.T) {
	cases := []struct {
		phase int64
		want  components.Season
	}{
		{0, components.SeasonSpring},
		{2399, components.SeasonSpring},
		{2400, components.SeasonSummer},
		{4799, components.SeasonSummer},
		{4800, components.SeasonAutumn},
		{7199, components.SeasonAutumn},
		{7200, components.SeasonWinter},
		{9599, components.SeasonWinter},
		{9600, components.SeasonSpring}, // 新年回到春季
	}
	for _, c := range cases {
		if got := components.SeasonOf(c.phase, 9600); got != c.want {
			t.Fatalf("SeasonOf(%d) = %v, want %v", c.phase, got, c.want)
		}
	}
}

// 地形/高度修正：水边更湿冷、雪地更冷、高度越高越冷。
func TestWeatherAtTerrainModifiers(t *testing.T) {
	s := NewSampler(42)
	q := sampleQ(5, 5, 100)
	grass := s.WeatherAt(q)

	qw := q
	qw.TileType = game.TerrainType_TERRAIN_TYPE_WATER
	water := s.WeatherAt(qw)
	if water.Temperature >= grass.Temperature || water.Fog <= grass.Fog || water.Rain <= grass.Rain {
		t.Fatalf("水边应更湿冷: grass=%+v water=%+v", grass, water)
	}

	qh := q
	qh.Height = 8
	high := s.WeatherAt(qh)
	if high.Temperature >= grass.Temperature {
		t.Fatalf("高处应更冷: grass=%+v high=%+v", grass, high)
	}
}
