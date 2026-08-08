package world

import (
	"sync"
	"testing"
	"time"

	pb "google.golang.org/protobuf/proto"
	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// newM5World 创建带推送捕获的世界（M5 测试用）。
func newM5World(t *testing.T, cfg WorldConfig) (*actor.Engine, *actor.PID, *WorldActor, func() []PushEffect) {
	t.Helper()
	eng := actor.NewEngine(actor.Config{})
	wa := NewWorldActor(cfg)
	pid := eng.Spawn(func() actor.IActor { return wa }, "world", "room-1")

	var mu sync.Mutex
	var pushed []PushEffect
	wa.SetPushSink(func(ef PushEffect) {
		mu.Lock()
		pushed = append(pushed, ef)
		mu.Unlock()
	})
	snapshot := func() []PushEffect {
		mu.Lock()
		defer mu.Unlock()
		return append([]PushEffect(nil), pushed...)
	}
	t.Cleanup(eng.Shutdown)
	return eng, pid, wa, snapshot
}

func createPlayer(t *testing.T, eng *actor.Engine, pid *actor.PID, uid string) ecs.Entity {
	t.Helper()
	resp := eng.Request(pid, CreatePlayer{UID: uid}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	return v.(ecs.Entity)
}

// 从增量快照找某实体的某个组件值（返回最新的匹配，轮询时可看到状态演进）。
func deltaComponent(t *testing.T, deltas []PushEffect, entity ecs.Entity, comp string) ([]byte, bool) {
	t.Helper()
	var latest []byte
	found := false
	for _, ef := range deltas {
		d, ok := ef.Payload.(*game.SnapshotDelta)
		if !ok {
			continue
		}
		for _, es := range d.Entities {
			if es.EntityId == uint64(entity) {
				for _, cs := range es.Components {
					if cs.Component == comp {
						latest = cs.Data
						found = true
					}
				}
			}
		}
	}
	return latest, found
}

func TestFullSnapshot(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	createPlayer(t, eng, pid, "u1")
	tree := wa.sim.CreateEntity()
	ecs.Add(wa.sim, tree, components.Position{X: 5, Y: 5})
	ecs.Add(wa.sim, tree, components.Health{Cur: 50, Max: 50})
	ecs.Add(wa.sim, tree, components.Growable{Stage: 0})

	snap := FullSnapshot(wa.sim)
	if len(snap.Entities) != 2 {
		t.Fatalf("entities = %d, want 2", len(snap.Entities))
	}
	// 玩家（ID 1）应有 Position/Health/Hunger
	playerState := snap.Entities[0]
	if playerState.EntityId != 1 {
		t.Fatalf("first entity = %d", playerState.EntityId)
	}
	comps := map[string]bool{}
	for _, cs := range playerState.Components {
		comps[cs.Component] = true
	}
	for _, want := range []string{"Position", "Health", "Hunger"} {
		if !comps[want] {
			t.Fatalf("player missing %s, got %v", want, comps)
		}
	}
}

func TestDeltaAfterMove(t *testing.T) {
	eng, pid, _, pushed := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")

	eng.Send(pid, Command{UID: "u1", Kind: CommandMove,
		Data: MoveData{Entity: player, DX: 2, DY: 3}})
	eng.Send(pid, Tick{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		data, ok := deltaComponent(t, pushed(), player, "Position")
		if ok {
			var p game.Position
			if err := pb.Unmarshal(data, &p); err == nil && p.X == 2 && p.Y == 3 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no Position delta after move")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHungerDeath(t *testing.T) {
	eng, pid, _, pushed := newM5World(t, WorldConfig{HungerRate: 10})
	player := createPlayer(t, eng, pid, "u1")

	// 饥饿 100 速率 10 → 10 tick 饿到 0；之后饿血每 tick -1 → 约 110 tick 死亡
	for i := 0; i < 115; i++ {
		eng.Send(pid, Tick{})
	}
	// 通过增量快照观察：死亡 = 实体出现 Dead 组件（不销毁）
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := deltaComponent(t, pushed(), player, "Dead"); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("player not dead")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGrowth(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, WorldConfig{GrowthTicks: 5})
	tree := wa.sim.CreateEntity()
	ecs.Add(wa.sim, tree, components.Position{X: 1, Y: 1})
	ecs.Add(wa.sim, tree, components.Health{Cur: 50, Max: 50})
	ecs.Add(wa.sim, tree, components.Growable{Stage: 0})

	for i := 0; i < 6; i++ {
		eng.Send(pid, Tick{})
	}
	// 通过增量快照观察树的 Growable 阶段
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, ok := deltaComponent(t, pushed(), tree, "Growable"); ok {
			var g game.Growable
			if pb.Unmarshal(data, &g) == nil && g.Stage >= 1 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("tree did not grow")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAttack(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, WorldConfig{AttackDamage: 20})
	attacker := createPlayer(t, eng, pid, "u1")
	tree := wa.sim.CreateEntity()
	ecs.Add(wa.sim, tree, components.Position{X: 0, Y: 1}) // 相邻
	ecs.Add(wa.sim, tree, components.Health{Cur: 50, Max: 50})

	attack := func() {
		eng.Send(pid, Command{UID: "u1", Kind: CommandAttack,
			Data: AttackData{Attacker: attacker, Target: tree}})
		eng.Send(pid, Tick{})
	}
	attack()
	attack()
	// 50 - 20*2 = 10 血，还没死
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, ok := deltaComponent(t, pushed(), tree, "Health"); ok {
			var h game.Health
			if pb.Unmarshal(data, &h) == nil && h.Cur == 10 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("tree health not reduced")
		}
		time.Sleep(time.Millisecond)
	}
	attack()
	// 再打一次 → 树血归零，出现 Dead 组件（不销毁）
	deadline = time.Now().Add(2 * time.Second)
	for {
		if _, ok := deltaComponent(t, pushed(), tree, "Dead"); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("tree not dead after 3 attacks")
		}
		time.Sleep(time.Millisecond)
	}
}
