package world

import (
	"errors"
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
	// 走到 (3,4)（tick 制移动，逐格推进）
	moveTo(t, eng1, pid1, "u1", p1, 3, 4)
	syncWorld(t, eng1, pid1)

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
	moveTo(t, eng2, pid2, "u1", p1, 4, 4)
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

// TestSaveViaRequestWhileTicking：tick 运行中经 SaveRequest（actor 消息）保存。
func TestSaveViaRequestWhileTicking(t *testing.T) {
	eng, pid, _, _ := newM5World(t, WorldConfig{})
	createPlayer(t, eng, pid, "u1")

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				eng.Send(pid, Tick{})
				time.Sleep(time.Millisecond)
			}
		}
	}()
	defer close(stop)

	// tick 运行期间反复经 SaveRequest 保存（actor 线性）
	for i := 0; i < 20; i++ {
		resp := eng.Request(pid, SaveRequest{}, time.Second)
		v, err := resp.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if len(v.([]byte)) == 0 {
			t.Fatal("empty save")
		}
	}
	// 最后一份存档可加载
	resp := eng.Request(pid, SaveRequest{}, time.Second)
	v, _ := resp.Wait()
	wa2 := NewWorldActor(WorldConfig{})
	if err := wa2.Load(v.([]byte)); err != nil {
		t.Fatal(err)
	}
}

// TestSaveNow：事件触发入口（如每天开始）经注入的 sink 落盘，数据可加载。
func TestSaveNow(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	createPlayer(t, eng, pid, "u1")

	var mu sync.Mutex
	var saved []byte
	wa.SetSaveSink(func(data []byte) error {
		mu.Lock()
		saved = append(saved, data...)
		mu.Unlock()
		return nil
	})
	wa.SaveNow()

	mu.Lock()
	ok := len(saved) > 0
	data := append([]byte(nil), saved...)
	mu.Unlock()
	if !ok {
		t.Fatal("save sink not called")
	}
	wa2 := NewWorldActor(WorldConfig{})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}
}

func TestSaveObserverReportsTriggerBytesAndSinkError(t *testing.T) {
	_, _, wa, _ := newM5World(t, WorldConfig{})
	wantErr := errors.New("disk full")
	observed := make(chan SaveStats, 1)
	wa.SetSaveObserver(SaveObserverFunc(func(stats SaveStats) {
		observed <- stats
	}))
	wa.SetSaveSink(func([]byte) error { return wantErr })

	wa.SaveNow()
	stats := <-observed
	if stats.Trigger != SaveTriggerEvent {
		t.Fatalf("trigger = %q, want %q", stats.Trigger, SaveTriggerEvent)
	}
	if stats.Bytes <= 0 || stats.Duration <= 0 {
		t.Fatalf("stats = %+v, want positive bytes and duration", stats)
	}
	if !errors.Is(stats.Err, wantErr) {
		t.Fatalf("err = %v, want %v", stats.Err, wantErr)
	}
}

func TestSaveObserverReportsManualAndShutdownTriggers(t *testing.T) {
	_, _, wa, _ := newM5World(t, WorldConfig{})
	observed := make(chan SaveStats, 2)
	wa.SetSaveObserver(SaveObserverFunc(func(stats SaveStats) {
		observed <- stats
	}))

	if data := wa.Save(); len(data) == 0 {
		t.Fatal("manual save returned empty payload")
	}
	manual := <-observed
	if manual.Trigger != SaveTriggerManual || manual.Bytes <= 0 || manual.Err != nil {
		t.Fatalf("manual stats = %+v", manual)
	}

	if data := wa.SaveWithTrigger(SaveTriggerShutdown); len(data) == 0 {
		t.Fatal("shutdown save returned empty payload")
	}
	shutdown := <-observed
	if shutdown.Trigger != SaveTriggerShutdown || shutdown.Bytes <= 0 || shutdown.Err != nil {
		t.Fatalf("shutdown stats = %+v", shutdown)
	}
}
