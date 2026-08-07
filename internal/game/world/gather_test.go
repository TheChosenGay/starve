package world

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// addBush 在世界上摆一个浆果丛（测试辅助）。
func addBush(t *testing.T, wa *WorldActor, x, y, count int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Gatherable{Kind: components.ResourceBerry, Count: count})
	return e
}

// syncWorld 发一条查询消息做同步点：等它返回时，前面的命令+tick 都已处理完。
func syncWorld(t *testing.T, eng *actor.Engine, pid *actor.PID) {
	t.Helper()
	resp := eng.Request(pid, QueryWorldTime{}, time.Second)
	if _, err := resp.Wait(); err != nil {
		t.Fatal(err)
	}
}

// TestGather：采集成功 → 玩家背包 berry+1，目标 Count 3→2。
func TestGather(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	bush := addBush(t, wa, 0, 1, 3)

	eng.Send(pid, Command{UID: "u1", Kind: CommandGather,
		Data: GatherData{Player: player, Target: bush}})
	eng.Send(pid, Tick{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, ok := deltaComponent(t, pushed(), player, "Inventory"); ok {
			var inv game.Inventory
			if pb.Unmarshal(data, &inv) == nil && invCount(&inv, game.ResourceKind_RESOURCE_KIND_BERRY) == 1 {
				if data2, ok2 := deltaComponent(t, pushed(), bush, "Gatherable"); ok2 {
					var g game.Gatherable
					if pb.Unmarshal(data2, &g) == nil && g.Kind == game.ResourceKind_RESOURCE_KIND_BERRY && g.Count == 2 {
						return
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("gather did not apply")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestGatherDepletedRemovesComponent：Count=1 采一次耗尽 → 组件被移除，
// 增量快照携带 RemovedComponents 告知客户端。
func TestGatherDepletedRemovesComponent(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	bush := addBush(t, wa, 0, 1, 1)

	eng.Send(pid, Command{UID: "u1", Kind: CommandGather,
		Data: GatherData{Player: player, Target: bush}})
	eng.Send(pid, Tick{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, ef := range pushed() {
			d, ok := ef.Payload.(*game.SnapshotDelta)
			if !ok {
				continue
			}
			for _, rc := range d.RemovedComponents {
				if rc.EntityId != uint64(bush) {
					continue
				}
				for _, c := range rc.Components {
					if c == "Gatherable" {
						if !ecs.Has[components.Gatherable](wa.sim, bush) {
							return
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("Gatherable not removed after depletion")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestGatherTooFar：距离不够 → 无效果。
func TestGatherTooFar(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	bush := addBush(t, wa, 5, 5, 3)

	eng.Send(pid, Command{UID: "u1", Kind: CommandGather,
		Data: GatherData{Player: player, Target: bush}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	inv := ecs.Get[components.Inventory](wa.sim, player)
	if len(inv.Resources) != 0 {
		t.Fatalf("player inventory = %v, want empty", inv.Resources)
	}
	g := ecs.Get[components.Gatherable](wa.sim, bush)
	if g.Count != 3 {
		t.Fatalf("bush count = %d, want 3", g.Count)
	}
}

// TestGatherNotOwned：其他 UID 操作别人的实体 → 无效果。
func TestGatherNotOwned(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	bush := addBush(t, wa, 0, 1, 3)

	eng.Send(pid, Command{UID: "u2", Kind: CommandGather,
		Data: GatherData{Player: player, Target: bush}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	inv := ecs.Get[components.Inventory](wa.sim, player)
	if len(inv.Resources) != 0 {
		t.Fatalf("player inventory = %v, want empty", inv.Resources)
	}
	g := ecs.Get[components.Gatherable](wa.sim, bush)
	if g.Count != 3 {
		t.Fatalf("bush count = %d, want 3", g.Count)
	}
}

// TestResourceSeedsFromConfig：资源配置表加载 + seed 出可采集实体。
func TestResourceSeedsFromConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.json")
	data := `[{"kind":"berry","x":1,"y":2,"count":3},{"kind":"flint","x":4,"y":5,"count":2}]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	wa := NewWorldActor(WorldConfig{ResourcesPath: path})
	var kinds []components.ResourceKind
	var counts int
	ecs.Query[components.Gatherable](wa.sim, func(e ecs.Entity, g *components.Gatherable) {
		kinds = append(kinds, g.Kind)
		counts += g.Count
	})
	if len(kinds) != 2 {
		t.Fatalf("seeded %d gatherable, want 2 (%v)", len(kinds), kinds)
	}
	if counts != 5 {
		t.Fatalf("total count = %d, want 5", counts)
	}
}

// TestResourceConfigMissingFallsBack：配置文件缺失 → 跳过 seed，不崩溃。
func TestResourceConfigMissingFallsBack(t *testing.T) {
	wa := NewWorldActor(WorldConfig{ResourcesPath: filepath.Join(t.TempDir(), "nope.json")})
	n := 0
	ecs.Query[components.Gatherable](wa.sim, func(e ecs.Entity, g *components.Gatherable) { n++ })
	if n != 0 {
		t.Fatalf("seeded %d gatherable, want 0", n)
	}
}

// invCount 从 proto Inventory 里查某资源的数量（无则 0）。
func invCount(inv *game.Inventory, kind game.ResourceKind) int32 {
	for _, rc := range inv.Resources {
		if rc.Kind == kind {
			return rc.Count
		}
	}
	return 0
}
