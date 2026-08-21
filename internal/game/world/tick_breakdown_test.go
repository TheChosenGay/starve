package world

import (
	"fmt"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/systems"
)

// realWorldCfg 与 cmd/gate 默认一致的配置（真实地图/生物/天气）。
func realWorldCfg() WorldConfig {
	return WorldConfig{
		MapPath:           "../../../configs/map.json",
		BiomesPath:        "../../../configs/biomes.json",
		CreaturesPath:     "../../../configs/creatures.json",
		TemplatesPath:     "../../../configs/resource_templates.json",
		RecipesPath:       "../../../configs/crafting.json",
		StationsPath:      "../../../configs/stations.json",
		WeatherPath:       "../../../configs/weather.json",
		WeatherFrameTicks: 20,
		MapSeed:           42,
	}
}

// TestRealTickBreakdown：真实服务器配置下一个 tick 的耗时分解（逐系统 + 世界层步骤）。
func TestRealTickBreakdown(t *testing.T) {
	if testing.Short() {
		t.Skip("tick breakdown")
	}
	wa := NewWorldActor(realWorldCfg())
	for i := 0; i < 2; i++ {
		wa.createPlayer(fmt.Sprintf("p%d", i))
	}
	for i := 0; i < 10; i++ {
		tickWorld(wa) // 预热（生物感知/仇恨建立）
	}

	type part struct {
		name string
		avg  time.Duration
	}
	var parts []part
	const n = 300
	measure := func(name string, fn func()) {
		start := time.Now()
		for i := 0; i < n; i++ {
			fn()
		}
		parts = append(parts, part{name, time.Since(start) / n})
	}

	// 玩法系统逐一计时（与 RegisterAll 一致）
	type update interface {
		Update(*ecs.World, time.Duration)
	}
	sysList := []struct {
		name string
		s    update
	}{
		{"daynight", &systems.DayNightSystem{}},
		{"weather", &systems.WeatherSystem{}},
		{"effect", &systems.EffectSystem{}},
		{"aoi", &systems.AOISystem{}},
		{"ai", &systems.AISystem{}},
		{"control", &systems.ControlSystem{}},
		{"action", &systems.ActionSystem{}},
		{"move", &systems.MoveSystem{}},
		{"hunger", &systems.HungerSystem{}},
		{"starvation", &systems.StarvationSystem{HealthDrain: 1}},
		{"growth", &systems.GrowthSystem{TicksPerStage: 20}},
		{"respawn", &systems.RespawnSystem{}},
		{"craft", &systems.CraftSystem{}},
		{"death", &systems.DeathSystem{}},
	}
	for _, s := range sysList {
		measure(s.name, func() { s.s.Update(wa.sim, wa.cfg.TickInterval) })
	}
	// 世界层步骤（onTick 里 RunSystems 之外的部分）
	measure("worldSteps", func() {
		wa.cmds.applyActionCommits()
		wa.completeCrafts()
		wa.processDrops()
		wa.stampDead()
		wa.cleanupCorpses()
		wa.cleanupOffline()
	})
	measure("snapshotDelta", func() {
		removed := wa.drainRemoved()
		dirty := wa.sim.DrainDirtySorted()
		DeltaSnapshot(wa.sim, dirty, removed)
	})
	// weatherFrame 每 WeatherFrameTicks 才推一次，按真实节奏摊销到每 tick
	start := time.Now()
	for i := 0; i < n; i++ {
		if wa.tick%int64(wa.cfg.WeatherFrameTicks) == 0 {
			wa.maybePushWeatherFrame()
		}
		wa.tick++
	}
	parts = append(parts, part{"weatherFrame(摊销)", time.Since(start) / n})

	total := time.Duration(0)
	for _, p := range parts {
		total += p.avg
		t.Logf("%-14s %9.2f µs", p.name, float64(p.avg.Microseconds()))
	}
	t.Logf("%-14s %9.2f µs（%.2f%% of 50ms tick）", "合计", float64(total.Microseconds()), float64(total)*100/float64(50*time.Millisecond))
}
