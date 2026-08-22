package world

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

func TestHauntJournalDecodeAndReplayDeterministic(t *testing.T) {
	mapPath := filepath.Join(t.TempDir(), "map.json")
	mapJSON := `{
		"width": 8, "height": 8, "spawn_x": 2, "spawn_y": 2,
		"terrain": {
			"hills": 1, "max_amp": 1, "rock_level": 6,
			"water_level": 0, "spawn_flat_radius": 2, "base_level": 3
		},
		"handplaced": {
			"revival_statues": [{"x": 3, "y": 2, "uses": 1, "duration_ticks": 1}]
		}
	}`
	if err := os.WriteFile(mapPath, []byte(mapJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := WorldConfig{MapPath: mapPath, HungerRate: 100, CorpseRetentionTicks: 1}

	probe := NewWorldActor(cfg)
	player := probe.createPlayer("u1")
	var statue ecs.Entity
	ecs.Query[components.Hauntable](probe.sim, func(e ecs.Entity, _ *components.Hauntable) {
		statue = e
	})
	if statue == 0 {
		t.Fatal("测试地图未生成复活雕像")
	}
	raw, err := json.Marshal(HauntData{Player: player, Target: statue})
	if err != nil {
		t.Fatal(err)
	}
	entry := JournalEntry{
		Tick: 2005, UID: "u1", Seq: 1, RequestID: 77,
		Kind: CommandHaunt, Data: raw,
	}
	decoded, ok := entry.decodeData().(HauntData)
	if !ok || decoded.Player != player || decoded.Target != statue {
		t.Fatalf("HauntData journal decode=%+v", decoded)
	}
	entries := []JournalEntry{
		{Tick: 0, UID: "u1", Kind: JournalJoin},
		entry,
	}

	first := NewWorldActor(cfg)
	second := NewWorldActor(cfg)
	first.Replay(entries, 2008)
	second.Replay(entries, 2008)
	if !pb.Equal(FullSnapshot(first.sim), FullSnapshot(second.sim)) {
		t.Fatal("相同作祟 journal 重放结果不一致")
	}
	if ecs.Has[components.Dead](first.sim, player) {
		t.Fatal("重放后玩家应已通过雕像复活")
	}
	if first.sim.IsAlive(statue) {
		t.Fatal("重放后最后一次雕像应已消耗")
	}
}
