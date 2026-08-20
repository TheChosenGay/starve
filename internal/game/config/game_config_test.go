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
