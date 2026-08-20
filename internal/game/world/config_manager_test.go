package world

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/config"
	"starve/internal/game/worldmap"
)

// ConfigManager：路径/运行时参数映射 + 加载 + 枚举，全流程可用。
func TestConfigManager(t *testing.T) {
	dir := t.TempDir()
	mapPath := filepath.Join(dir, "map.json")
	if err := os.WriteFile(mapPath, []byte(regionTestMapJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	biomesPath := filepath.Join(dir, "biomes.json")
	if err := os.WriteFile(biomesPath, []byte(regionTestBiomesJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	m := config.NewConfigManager()
	m.TickInterval = 50 * time.Millisecond
	m.OfflineSeconds = 300
	m.CorpseSeconds = 60
	m.InventorySlots = 20
	m.MapSeed = 42
	m.MoveSpeed = 10
	m.SetPath(config.ConfigMap, mapPath)
	m.SetPath(config.ConfigBiomes, biomesPath)

	// 枚举
	if got := len(m.Kinds()); got != 9 {
		t.Fatalf("Kinds 数量 = %d, want 9", got)
	}
	// WorldConfig 映射（20Hz 下秒→tick）
	cfg := m.WorldConfig()
	if cfg.MapPath != mapPath || cfg.BiomesPath != biomesPath {
		t.Fatalf("路径未映射: map=%q biomes=%q", cfg.MapPath, cfg.BiomesPath)
	}
	if cfg.OfflineRetentionTicks != 6000 || cfg.CorpseRetentionTicks != 1200 {
		t.Fatalf("保留 tick 换算错误: offline=%d corpse=%d", cfg.OfflineRetentionTicks, cfg.CorpseRetentionTicks)
	}
	// 加载
	gc, err := m.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.Biomes) != 5 {
		t.Fatalf("biomes = %d, want 5", len(gc.Biomes))
	}
	if gc.MapSpec == nil || gc.MapSpec.RegionLayout == nil {
		t.Fatal("map 配置应包含区域布局")
	}

	// 外部加载配置 → NewWorldActorWithConfig（避免二次加载）
	wa := NewWorldActorWithConfig(cfg, gc)
	md := ecs.Resource[worldmap.MapData](wa.sim)
	if len(md.RegionIDs) != 60*60 {
		t.Fatalf("区域 id 表未生成: %d", len(md.RegionIDs))
	}
}
