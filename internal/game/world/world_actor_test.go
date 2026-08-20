package world

import (
	"sync"
	"testing"
	"time"

	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

// msgCollector 收集收到的消息，用于断言 outbox 投递。
type msgCollector struct {
	mu   sync.Mutex
	msgs []any
}

func (c *msgCollector) Receive(ctx actor.IActorContext) {
	c.mu.Lock()
	c.msgs = append(c.msgs, ctx.Message())
	c.mu.Unlock()
}

func (c *msgCollector) waitCount(n int, t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		c.mu.Lock()
		got := len(c.msgs)
		c.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("collector got %d, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

// queryPos 通过请求-应答查询实体位置（走 actor 邮箱，天然与 tick 串行）。
func queryPos(t *testing.T, eng *actor.Engine, pid *actor.PID, e ecs.Entity) components.Position {
	t.Helper()
	resp := eng.Request(pid, QueryPosition{Entity: e}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	pos, ok := v.(components.Position)
	if !ok {
		t.Fatalf("query = %#v", v)
	}
	return pos
}

func waitPos(t *testing.T, eng *actor.Engine, pid *actor.PID, e ecs.Entity, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if queryPos(t, eng, pid, e).X == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("position.X = %d, want %d", queryPos(t, eng, pid, e).X, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func newTestWorld(t *testing.T, cfg WorldConfig) (*actor.Engine, *actor.PID, *WorldActor) {
	t.Helper()
	eng := actor.NewEngine(actor.Config{})
	wa := NewWorldActor(cfg)
	pid := eng.Spawn(func() actor.IActor { return wa }, "world", "room-1")
	t.Cleanup(eng.Shutdown)
	return eng, pid, wa
}

func TestCommandSeqDropsDuplicatesAndStale(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	wa.players[e] = "1"

	eng.Send(pid, BeginInputEpoch{UID: "1", Epoch: 9})
	eng.Send(pid, Command{UID: "1", InputEpoch: 9, Seq: 1, Kind: CommandMove, Data: MoveData{Entity: e, DX: 1, DY: 0}})
	eng.Send(pid, Command{UID: "1", InputEpoch: 9, Seq: 1, Kind: CommandMove, Data: MoveData{Entity: e, DX: -1, DY: 0}})
	eng.Send(pid, Command{UID: "1", InputEpoch: 8, Seq: 2, Kind: CommandMove, Data: MoveData{Entity: e, DX: -1, DY: 0}})
	eng.Send(pid, Command{UID: "1", Seq: 0, Kind: CommandMove, Data: MoveData{Entity: e, DX: 0, DY: 1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	mv := ecs.Get[components.Moveable](wa.sim, e)
	if mv.DirX != 0 || mv.DirY != 1 {
		t.Fatalf("dir = (%d,%d), want (0,1) after duplicate seq dropped and seq=0 applied", mv.DirX, mv.DirY)
	}
	if got := wa.inputAcks["1"]; got != (InputAck{Epoch: 9, Seq: 1}) {
		t.Fatalf("input ack = %+v, want epoch=9 seq=1", got)
	}
}

func TestCommandSeqAcknowledgesOnlyAcceptedMove(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	wa.players[e] = "owner"

	eng.Send(pid, BeginInputEpoch{UID: "other", Epoch: 4})
	eng.Send(pid, Command{
		UID: "other", InputEpoch: 4, Seq: 1, Kind: CommandMove,
		Data: MoveData{Entity: e, DX: 1, DY: 0},
	})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if got := wa.inputAcks["other"]; got.Seq != 0 {
		t.Fatalf("rejected move acknowledged as seq %d", got.Seq)
	}
}

func TestMoveStopPreservesFractionalPosition(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{
		TickInterval: 50 * time.Millisecond,
		MoveSpeed:    10,
	})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 5, Y: 3})
	wa.players[e] = "1"

	eng.Send(pid, BeginInputEpoch{UID: "1", Epoch: 2})
	eng.Send(pid, Command{
		UID: "1", InputEpoch: 2, Seq: 1, Kind: CommandMove,
		Data: MoveData{Entity: e, DX: 1},
	})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	beforeStop := ecs.Get[components.Moveable](wa.sim, e).SubX
	if beforeStop <= 0 || beforeStop >= 1 {
		t.Fatalf("moving sub = %v, want fractional position", beforeStop)
	}

	eng.Send(pid, Command{
		UID: "1", InputEpoch: 2, Seq: 2, Kind: CommandMove,
		Data: MoveData{Entity: e},
	})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	afterStop := ecs.Get[components.Moveable](wa.sim, e)
	if afterStop.DirX != 0 || afterStop.DirY != 0 || afterStop.SubX != beforeStop {
		t.Fatalf("stop state = %+v, want sub_x=%v preserved", afterStop, beforeStop)
	}
}

// TestCommandBuffering：100 条移动指令积攒后一个 tick 全部消费；
// 移动是"方向意图 + PositionSystem 推进"，不会一次性跳 100 格。
func TestCommandBuffering(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	wa.players[e] = "1"

	eng.Send(pid, BeginInputEpoch{UID: "1", Epoch: 1})
	for i := 0; i < 100; i++ {
		eng.Send(pid, Command{UID: "1", InputEpoch: 1, Seq: uint64(i + 1), Kind: CommandMove,
			Data: MoveData{Entity: e, DX: 1, DY: 0}})
	}
	if got := queryPos(t, eng, pid, e); got.X != 0 {
		t.Fatalf("commands applied before tick: X=%d", got.X)
	}

	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	mv := ecs.Get[components.Moveable](wa.sim, e)
	if mv.DirX != 1 || mv.DirY != 0 {
		t.Fatalf("tick 后输入方向应为(1,0)，got (%d,%d)", mv.DirX, mv.DirY)
	}
	// 默认速度 10 格/秒（0.5 格/tick）：继续 tick 连续移动，不是一次 100 格
	deadline := time.Now().Add(2 * time.Second)
	for {
		eng.Send(pid, Tick{})
		if queryPos(t, eng, pid, e).X >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("移动未按速度推进")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := queryPos(t, eng, pid, e); got.X > 10 {
		t.Fatalf("不应一次性跳远: X=%d", got.X)
	}
}

// TestTickSelfDriven：Start 后 SendRepeat 自驱动，世界时钟按 dt 前进。
func TestTickSelfDriven(t *testing.T) {
	eng, pid, _ := newTestWorld(t, WorldConfig{TickInterval: 20 * time.Millisecond})
	eng.Send(pid, Start{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := eng.Request(pid, QueryWorldTime{}, time.Second)
		v, err := resp.Wait()
		if err != nil {
			t.Fatal(err)
		}
		if wt := v.(time.Duration); wt >= 60*time.Millisecond {
			return // 至少 3 个 tick
		}
		if time.Now().After(deadline) {
			t.Fatal("world time did not advance")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestFlushOutbox：tick 结束时 Push/SendMessage 效果统一投递给目标 actor。
func TestFlushOutbox(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	collector := &msgCollector{}
	colPID := eng.Spawn(func() actor.IActor { return collector }, "svc", "collector")

	var mu sync.Mutex
	var pushed []PushEffect
	wa.SetPushSink(func(ef PushEffect) {
		mu.Lock()
		pushed = append(pushed, ef)
		mu.Unlock()
	})

	// 测试直塞 outbox（tick 前写入，无并发），模拟命令/系统产生的副作用
	wa.outbox = append(wa.outbox,
		PushEffect{To: "c1", Route: proto.RouteSnapshotDelta, Payload: &game.SnapshotDelta{}},
		SendMessageEffect{To: colPID, Msg: "hello"},
	)
	eng.Send(pid, Tick{})
	collector.waitCount(1, t) // SendMessage 到达 collector

	// 手动塞的 PushEffect 应先于系统生成的 delta 被投递
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		ok := len(pushed) > 0 && pushed[0].To == "c1" && pushed[0].Route == proto.RouteSnapshotDelta
		mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			mu.Lock()
			cp := append([]PushEffect(nil), pushed...)
			mu.Unlock()
			t.Fatalf("pushed = %v", cp)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestReplayDeterminism：同样的命令序列 → 同样的世界状态（接缝层回放）。
func TestReplayDeterminism(t *testing.T) {
	run := func() components.Position {
		eng, pid, wa := newTestWorld(t, WorldConfig{})
		e := wa.sim.CreateEntity()
		ecs.Add(wa.sim, e, components.Position{X: 1, Y: 1})
		for _, d := range [][2]int{{1, 0}, {1, 1}, {-1, 0}, {0, 1}, {-1, -1}} {
			eng.Send(pid, Command{Kind: CommandMove, Data: MoveData{Entity: e, DX: d[0], DY: d[1]}})
		}
		for i := 0; i < 10; i++ {
			eng.Send(pid, Tick{})
		}
		syncWorld(t, eng, pid)
		return queryPos(t, eng, pid, e)
	}
	p1, p2 := run(), run()
	if p1 != p2 {
		t.Fatalf("replay differs: %+v vs %+v", p1, p2)
	}
}
