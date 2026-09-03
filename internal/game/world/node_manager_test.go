package world

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TheChosenGay/actor"
)

// mockRoom 是测试用的房间 actor：记录收到的消息。
type mockRoom struct {
	mu   sync.Mutex
	msgs []any
}

func (r *mockRoom) Receive(ctx actor.IActorContext) {
	r.mu.Lock()
	r.msgs = append(r.msgs, ctx.Message())
	r.mu.Unlock()
}

func (r *mockRoom) got(want any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.msgs {
		if reflect.TypeOf(m) == reflect.TypeOf(want) {
			return true
		}
	}
	return false
}

func waitRoomMsg(t *testing.T, r *mockRoom, want any) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.got(want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("room did not receive %T", want)
}

// TestNodeManagerCreateQueryDestroy：创建 → QueryToken → QueryWorlds → 销毁。
func TestNodeManagerCreateQueryDestroy(t *testing.T) {
	e := actor.NewEngine(actor.Config{})
	defer e.Shutdown()

	room := &mockRoom{}
	nmPID := e.Spawn(func() actor.IActor {
		return NewNodeManager(func(meta RoomMeta) actor.IActor { return room }, "")
	}, "node", "manager")

	// 创建房间。
	resp := e.Request(nmPID, CreateWorld{RoomName: "room-1", AccessToken: "tk1"}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatalf("create err: %v", err)
	}
	cr, ok := v.(CreateWorldResp)
	if !ok || cr.PID == nil {
		t.Fatalf("create resp = %#v", v)
	}
	waitRoomMsg(t, room, Start{})

	// QueryToken 命中。
	q := e.Request(nmPID, QueryToken{AccessToken: "tk1"}, time.Second)
	v2, err := q.Wait()
	if err != nil {
		t.Fatalf("query token err: %v", err)
	}
	qt := v2.(QueryTokenResp)
	if !qt.OK || qt.PID == nil || qt.RoomName != "room-1" {
		t.Fatalf("query token resp = %#v", v2)
	}

	// QueryWorlds 1 个。
	qw := e.Request(nmPID, QueryWorlds{}, time.Second)
	v3, _ := qw.Wait()
	if wr := v3.(QueryWorldsResp); len(wr.Rooms) != 1 {
		t.Fatalf("worlds = %d, want 1", len(wr.Rooms))
	}

	// 销毁后 QueryWorlds 0 个。
	e.Send(nmPID, DestroyWorld{RoomName: "room-1"})
	waitRoomMsg(t, room, Shutdown{})
	qw2 := e.Request(nmPID, QueryWorlds{}, time.Second)
	v4, _ := qw2.Wait()
	if wr := v4.(QueryWorldsResp); len(wr.Rooms) != 0 {
		t.Fatalf("worlds after destroy = %d, want 0", len(wr.Rooms))
	}
}
