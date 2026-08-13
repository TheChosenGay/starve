package world

import (
	"os"
	"path/filepath"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/weather"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

// 寒冷挂钩：温度 ≤ cold_at → 挂寒冷效果，每 tick 按 cold_damage 扣血。
func TestWeatherColdEffect(t *testing.T) {
	wa := newWeatherWorld(t, `{"year_ticks": 9600, "cold_at": 100, "cold_damage": 2, "heat_at": 100, "heat_damage": 1}`)
	e := wa.createPlayer("u1")
	hp := ecs.Get[components.Health](wa.sim, e)
	eff := ecs.Get[components.Effects](wa.sim, e)

	tickWorld(wa)
	st := eff.Active[components.EffectCold]
	if st.Count != 1 || st.Param != 2 {
		t.Fatalf("寒冷状态=%+v want count=1 param=2", st)
	}
	if hp.Cur != 98 {
		t.Fatalf("寒冷 tick: hp=%d want 98", hp.Cur)
	}
	tickWorld(wa)
	if hp.Cur != 96 {
		t.Fatalf("寒冷第二 tick: hp=%d want 96", hp.Cur)
	}
}

// 炎热挂钩：温度 ≥ heat_at → 挂炎热效果。
func TestWeatherHeatEffect(t *testing.T) {
	wa := newWeatherWorld(t, `{"year_ticks": 9600, "cold_at": -100, "cold_damage": 0, "heat_at": -100, "heat_damage": 3}`)
	e := wa.createPlayer("u1")
	hp := ecs.Get[components.Health](wa.sim, e)
	eff := ecs.Get[components.Effects](wa.sim, e)

	tickWorld(wa)
	st := eff.Active[components.EffectHeat]
	if st.Count != 1 || st.Param != 3 {
		t.Fatalf("炎热状态=%+v want count=1 param=3", st)
	}
	if hp.Cur != 97 {
		t.Fatalf("炎热 tick: hp=%d want 97", hp.Cur)
	}
}

// 风扇局部修正：下风向风力增强、雾密度降低。
func TestWeatherFanModifier(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	fan := wa.sim.CreateEntity()
	ecs.Add(wa.sim, fan, components.Position{X: 0, Y: 0})
	ecs.Add(wa.sim, fan, components.Fan{Strength: 5, DirX: 1, DirY: 0, Radius: 5})

	q := weather.WeatherQuery{X: 3, Y: 0, Tick: 100, Season: components.SeasonSpring}
	base := weather.NewSampler(42).WeatherAt(q)
	with := weather.SampleAt(wa.sim, q)
	if with.WindSpeed <= base.WindSpeed {
		t.Fatalf("风扇应增强下风向风速: base=%v with=%v", base.WindSpeed, with.WindSpeed)
	}
	if with.Fog >= base.Fog {
		t.Fatalf("风扇应降低下风向雾: base=%v with=%v", base.Fog, with.Fog)
	}
}

// 存档往返：天气相位随档保存/恢复（季节由相位推导，无需额外字段）。
func TestWeatherSaveLoadPhase(t *testing.T) {
	wa := NewWorldActor(WorldConfig{MapPath: testMapPath(t), MapSeed: 42})
	for i := 0; i < 5; i++ {
		tickWorld(wa)
	}
	wr := ecs.Resource[components.Weather](wa.sim)
	if wr.Phase != 5 {
		t.Fatalf("天气相位=%d want 5", wr.Phase)
	}
	data := wa.Save()

	wa2 := NewWorldActor(WorldConfig{})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}
	wr2 := ecs.Resource[components.Weather](wa2.sim)
	if wr2.Phase != wr.Phase || wr2.Season() != wr.Season() {
		t.Fatalf("加载后天气相位=%d season=%v, want %d/%v", wr2.Phase, wr2.Season(), wr.Phase, wr.Season())
	}
}

// newWeatherWorld 构造带天气配置的世界（临时 weather.json）。
func newWeatherWorld(t *testing.T, json string) *WorldActor {
	t.Helper()
	p := filepath.Join(t.TempDir(), "weather.json")
	if err := os.WriteFile(p, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewWorldActor(WorldConfig{WeatherPath: p})
}

// 天气帧：按间隔推送，粗粒度网格覆盖全图 + 全局风（客户端渲染雾/雨的数据源）。
func TestWeatherFramePush(t *testing.T) {
	eng, pid, _, pushed := newM5World(t, WorldConfig{MapPath: testMapPath(t), MapSeed: 42, WeatherFrameTicks: 2})
	createPlayer(t, eng, pid, "u1")
	eng.Send(pid, Tick{})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	for _, ef := range pushed() {
		if ef.Route != proto.RouteWeatherFrame {
			continue
		}
		f, ok := ef.Payload.(*game.WeatherFrame)
		if !ok {
			t.Fatalf("payload 类型错误: %T", ef.Payload)
		}
		// 测试地图 20×20，cell_size=10 → 2×2 = 4 个单元
		if len(f.Cells) != 4 || f.CellSize != 10 || f.CellsPerRow != 2 {
			t.Fatalf("天气帧网格不符: cells=%d cell=%d per_row=%d", len(f.Cells), f.CellSize, f.CellsPerRow)
		}
		for i, c := range f.Cells {
			if c.Rain < 0 || c.Rain > 1 || c.Fog < 0 || c.Fog > 1 {
				t.Fatalf("cell[%d] 雨/雾越界: %+v", i, c)
			}
		}
		if f.WindSpeed <= 0 {
			t.Fatalf("应包含全局风速: %+v", f)
		}
		return
	}
	t.Fatal("未收到天气帧")
}
