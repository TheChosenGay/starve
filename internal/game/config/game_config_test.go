package config

import (
	"os"
	"path/filepath"
	"testing"
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
