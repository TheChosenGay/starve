package world

import (
	"sync"
	"testing"
	"time"

	pb "google.golang.org/protobuf/proto"
	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// TestSaveLoadRoundTrip：存档 → 加载后世界完全一致（实体/组件/昼夜/时钟/所有权）。
func TestSaveLoadRoundTrip(t *testing.T) {
	eng1, pid1, wa1, _ := newM5World(t, WorldConfig{HungerRate: 2})
	p1 := createPlayer(t, eng1, pid1, "u1")
	tree := wa1.sim.CreateEntity()
	ecs.Add(wa1.sim, tree, components.Position{X: 5, Y: 5})
	ecs.Add(wa1.sim, tree, components.Health{Cur: 50, Max: 50})
	ecs.Add(wa1.sim, tree, components.Growable{Stage: 0})

	// 跑几个 tick + 移动，让昼夜/饥饿/位置都产生状态
	for i := 0; i < 5; i++ {
		eng1.Send(pid1, Tick{})
	}
	eng1.Send(pid1, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: p1, DX: 3, DY: 4}})
	eng1.Send(pid1, Tick{})

	// 经 SaveRequest 取存档（串行，避免并发读）
	resp := eng1.Request(pid1, SaveRequest{}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	data := v.([]byte)
	if len(data) == 0 {
		t.Fatal("empty save")
	}

	// 新世界加载
	eng2, pid2, wa2, pushed2 := newM5World(t, WorldConfig{HungerRate: 2})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}

	if !pb.Equal(FullSnapshot(wa1.sim), FullSnapshot(wa2.sim)) {
		t.Fatal("snapshots differ after save/load")
	}
	if wa2.WorldTime() != wa1.WorldTime() {
		t.Fatalf("world time: %v vs %v", wa2.WorldTime(), wa1.WorldTime())
	}

	// 玩家所有权恢复：加载后的世界允许 u1 移动自己的实体（3+1, 4）
	eng2.Send(pid2, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: p1, DX: 1, DY: 0}})
	eng2.Send(pid2, Tick{})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data2, ok := deltaComponent(t, pushed2(), p1, "Position"); ok {
			var p game.Position
			if pb.Unmarshal(data2, &p) == nil && p.X == 4 && p.Y == 4 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("player cannot move after load")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSaveLoadIDContinuation：加载后新建实体的 ID 从游标延续，不冲突。
func TestSaveLoadIDContinuation(t *testing.T) {
	eng1, pid1, _, _ := newM5World(t, WorldConfig{})
	createPlayer(t, eng1, pid1, "u1") // 实体 1

	resp := eng1.Request(pid1, SaveRequest{}, time.Second)
	v, _ := resp.Wait()
	data := v.([]byte)

	eng2, pid2, wa2, _ := newM5World(t, WorldConfig{})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}
	p2 := createPlayer(t, eng2, pid2, "u2")
	if p2 != 2 {
		t.Fatalf("new entity id = %d, want 2（ID 游标恢复后不冲突）", p2)
	}
}

// TestSaveEffectTriggersSave：SaveEffect（事件触发存档）经注入的出口落盘，
// 且产出的数据可以加载回世界。
func TestSaveEffectTriggersSave(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	createPlayer(t, eng, pid, "u1")

	var mu sync.Mutex
	var saved []byte
	wa.SetSaveHandler(func(data []byte) {
		mu.Lock()
		saved = append(saved, data...)
		mu.Unlock()
	})

	wa.outbox = append(wa.outbox, SaveEffect{Reason: "day_start"})
	eng.Send(pid, Tick{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		ok := len(saved) > 0
		mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("SaveEffect did not trigger save")
		}
		time.Sleep(time.Millisecond)
	}

	// 产出数据可加载回新世界
	mu.Lock()
	data := append([]byte(nil), saved...)
	mu.Unlock()
	wa2 := NewWorldActor(WorldConfig{})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}
}
