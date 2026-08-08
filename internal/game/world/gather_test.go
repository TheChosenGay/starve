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

// addBush 在世界上摆一个浆果丛（可采集，Workable{Pick}）。
func addBush(t *testing.T, wa *WorldActor, x, y, work int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Workable{Kind: components.ResourceBerry, Action: components.WorkPick, WorkLeft: work, MaxWork: work})
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

// TestGather：采集成功 → 背包 berry+1，目标 WorkLeft 3→2。
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
				if data2, ok2 := deltaComponent(t, pushed(), bush, "Workable"); ok2 {
					var w game.Workable
					if pb.Unmarshal(data2, &w) == nil && w.Kind == game.ResourceKind_RESOURCE_KIND_BERRY && w.WorkLeft == 2 {
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

// TestGatherDepletedKeepsEntity：采空（WorkLeft=0）→ 浆果丛原地保留，不挂 Dead、不移除。
func TestGatherDepletedKeepsEntity(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	bush := addBush(t, wa, 0, 1, 2)

	for i := 0; i < 2; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandGather,
			Data: GatherData{Player: player, Target: bush}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if !wa.sim.IsAlive(bush) {
		t.Fatal("采空后浆果丛应保留")
	}
	if ecs.Has[components.Dead](wa.sim, bush) {
		t.Fatal("采空不应挂 Dead")
	}
	w := ecs.Get[components.Workable](wa.sim, bush)
	if w.WorkLeft != 0 {
		t.Fatalf("采空后 WorkLeft = %d, want 0", w.WorkLeft)
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
	if len(inv.Items) != 0 {
		t.Fatalf("player inventory = %v, want empty", inv.Items)
	}
	w := ecs.Get[components.Workable](wa.sim, bush)
	if w.WorkLeft != 3 {
		t.Fatalf("bush WorkLeft = %d, want 3", w.WorkLeft)
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
	if len(inv.Items) != 0 {
		t.Fatalf("player inventory = %v, want empty", inv.Items)
	}
	w := ecs.Get[components.Workable](wa.sim, bush)
	if w.WorkLeft != 3 {
		t.Fatalf("bush WorkLeft = %d, want 3", w.WorkLeft)
	}
}

// TestResourceSeedsFromConfig：资源配置表加载 + seed 出可交互实体（Workable）。
func TestResourceSeedsFromConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resources.json")
	data := `[{"kind":"berry","x":1,"y":2,"action":"pick","work":3},{"kind":"flint","x":4,"y":5,"action":"mine","work":2}]`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	wa := NewWorldActor(WorldConfig{ResourcesPath: path})
	var kinds []components.ResourceKind
	var totalWork int
	ecs.Query[components.Workable](wa.sim, func(e ecs.Entity, w *components.Workable) {
		kinds = append(kinds, w.Kind)
		totalWork += w.WorkLeft
	})
	if len(kinds) != 2 {
		t.Fatalf("seeded %d workable, want 2 (%v)", len(kinds), kinds)
	}
	if totalWork != 5 {
		t.Fatalf("total work = %d, want 5", totalWork)
	}
}

// TestResourceConfigMissingFallsBack：配置文件缺失 → 跳过 seed，不崩溃。
func TestResourceConfigMissingFallsBack(t *testing.T) {
	wa := NewWorldActor(WorldConfig{ResourcesPath: filepath.Join(t.TempDir(), "nope.json")})
	n := 0
	ecs.Query[components.Workable](wa.sim, func(e ecs.Entity, w *components.Workable) { n++ })
	if n != 0 {
		t.Fatalf("seeded %d workable, want 0", n)
	}
}

// invCount 从 proto Inventory 里查某资源的数量（无则 0）。
func invCount(inv *game.Inventory, kind game.ResourceKind) int32 {
	for _, s := range inv.Items {
		if s.Kind == kind {
			return s.Count
		}
	}
	return 0
}
