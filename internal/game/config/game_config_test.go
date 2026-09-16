package config

import (
	"os"
	"path/filepath"
	"testing"

	"starve/internal/game/components"
)

func TestMapStationsOverrideLegacyStationFile(t *testing.T) {
	dir := t.TempDir()
	mapPath := filepath.Join(dir, "map.json")
	stationPath := filepath.Join(dir, "stations.json")
	if err := os.WriteFile(mapPath, []byte(`{
		"width":16,"height":16,"spawn_x":8,"spawn_y":8,
		"handplaced":{"stations":[{"type":"workbench","x":7,"y":9}]}
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stationPath, []byte(`[
		{"type":"campfire","x":1,"y":1}
	]`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadGameConfig(WorldConfig{MapPath: mapPath, StationsPath: stationPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Stations) != 1 || cfg.Stations[0].Type != "workbench" ||
		cfg.Stations[0].X != 7 || cfg.Stations[0].Y != 9 {
		t.Fatalf("stations = %+v, want map handplaced station", cfg.Stations)
	}
}

func TestViewRadiusInContract(t *testing.T) {
	gc, err := LoadGameConfig(WorldConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if gc.ViewRadius != DefaultViewRadius {
		t.Fatalf("default view_radius = %d, want %d", gc.ViewRadius, DefaultViewRadius)
	}
	if gc.ToProto().ViewRadius != DefaultViewRadius {
		t.Fatalf("proto view_radius = %d", gc.ToProto().ViewRadius)
	}

	unlimited, err := LoadGameConfig(WorldConfig{ViewRadius: -1})
	if err != nil {
		t.Fatal(err)
	}
	if unlimited.ToProto().ViewRadius != -1 {
		t.Fatalf("unlimited proto view_radius = %d, want -1", unlimited.ToProto().ViewRadius)
	}

	custom, err := LoadGameConfig(WorldConfig{ViewRadius: 16})
	if err != nil {
		t.Fatal(err)
	}
	if custom.ToProto().ViewRadius != 16 {
		t.Fatalf("custom proto view_radius = %d, want 16", custom.ToProto().ViewRadius)
	}
	if gc.ToProto().ViewPreload != DefaultViewPreload {
		t.Fatalf("default view_preload = %d, want %d", gc.ToProto().ViewPreload, DefaultViewPreload)
	}
	if gc.ToProto().ViewRadiusMax != DefaultViewRadius {
		t.Fatalf("unset view_radius_max should equal view_radius %d, got %d", DefaultViewRadius, gc.ToProto().ViewRadiusMax)
	}

	ranged, err := LoadGameConfig(WorldConfig{ViewRadius: 16, ViewRadiusMax: 32})
	if err != nil {
		t.Fatal(err)
	}
	if ranged.ToProto().ViewRadius != 16 || ranged.ToProto().ViewRadiusMax != 32 {
		t.Fatalf("range proto = %d..%d, want 16..32", ranged.ToProto().ViewRadius, ranged.ToProto().ViewRadiusMax)
	}
	if InterestRadius(16, 32, 8) != 40 {
		t.Fatalf("interest = %d, want 40", InterestRadius(16, 32, 8))
	}
	if NormalizeViewRadiusMax(16, 8) != 16 {
		t.Fatalf("max below min should lift to min")
	}
}

func TestViewRadiusMaxFromEnv(t *testing.T) {
	t.Setenv("GATE_VIEW_RADIUS", "24")
	t.Setenv("GATE_VIEW_RADIUS_MAX", "32")
	t.Setenv("GATE_VIEW_PRELOAD", "8")
	m := NewConfigManagerFromEnv()
	if m.ViewRadiusMax != 32 {
		t.Fatalf("env view_radius_max = %d, want 32", m.ViewRadiusMax)
	}
	if InterestRadius(m.ViewRadius, m.ViewRadiusMax, m.ViewPreload) != 40 {
		t.Fatalf("env interest = %d, want 40", InterestRadius(m.ViewRadius, m.ViewRadiusMax, m.ViewPreload))
	}
}

// 真实配置契约：仇恨传播半径必须存在，且掠食者的传播范围要明显大于感知半径。
//
// 为什么单独测这个：感知半径决定"我能看见谁"（要小，保留潜行感），
// 仇恨传播半径决定"打一只狼，狼群多大范围响应"（要大）。两者共用一个值时
// 必然顾此失彼——实测狼感知半径 6 时，打一只只有 3/5 同伴响应，狼群形同虚设。
func TestCreatureThreatRadiusWiderThanPerception(t *testing.T) {
	tpls, err := loadCreatures("../../../configs/creatures.json")
	if err != nil {
		t.Fatalf("load creatures: %v", err)
	}
	if len(tpls) == 0 {
		t.Fatal("配置里应有生物模板")
	}
	for kind, tpl := range tpls {
		if tpl.ThreatRadius <= 0 {
			t.Fatalf("生物 %v 的 threat_radius 应 > 0（缺省回退到感知半径）", kind)
		}
		// 允许被动生物（感知 0）用较小的传播半径，但掠食者必须更广
		if tpl.AttackDamage > 0 && tpl.ThreatRadius <= tpl.PerceptionRadius {
			t.Fatalf("掠食者 %v 的仇恨传播半径(%d)应大于感知半径(%d)：否则"+
				"打一只只有身边极小范围响应，群体仇恨形同虚设",
				kind, tpl.ThreatRadius, tpl.PerceptionRadius)
		}
	}
	// 具体数值的回归：狼是演示里最主要的群体仇恨载体
	wolf, ok := tpls[components.CreatureWolf]
	if !ok {
		t.Fatal("配置里应有 wolf")
	}
	if wolf.ThreatRadius < 12 {
		t.Fatalf("狼的仇恨传播半径应 >= 12（实测 6 时只有 3/5 同伴响应），实际 %d",
			wolf.ThreatRadius)
	}
}
