package gateway

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TheChosenGay/actor"

	"starve/internal/game/world"
)

// gwMockRoom 是测试用的房间 actor。
type gwMockRoom struct {
	mu   sync.Mutex
	msgs []any
}

func (r *gwMockRoom) Receive(ctx actor.IActorContext) {
	r.mu.Lock()
	r.msgs = append(r.msgs, ctx.Message())
	r.mu.Unlock()
}

// TestGatewayRoomResolver：握手按 accessToken 经 NodeManager 解析到世界；worldFor 正确路由；未知回退默认。
func TestGatewayRoomResolver(t *testing.T) {
	_, engine, defaultWorld, _, gw := newTestGatewayFull(t, world.WorldConfig{})

	// NodeManager + 一个房间。
	nmPID := engine.Spawn(func() actor.IActor {
		return world.NewNodeManager(func(meta world.RoomMeta) actor.IActor { return &gwMockRoom{} }, "")
	}, "node", "manager")
	resp := engine.Request(nmPID, world.CreateWorld{RoomName: "room-1", AccessToken: "tk1"}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatalf("create world: %v", err)
	}
	created := v.(world.CreateWorldResp)

	// 网关 resolver：查询 NodeManager。
	gw.SetRoomResolver(func(token string) (*actor.PID, string, bool) {
		r := engine.Request(nmPID, world.QueryToken{AccessToken: token}, time.Second)
		vv, _ := r.Wait()
		qt := vv.(world.QueryTokenResp)
		return qt.PID, qt.RoomName, qt.OK
	})

	// 握手携带 access_token → connWorld 绑定到该世界。
	conn := &fakeConn{id: "c1"}
	if _, err := gw.OnHandshake(context.Background(), conn, []byte(`{"access_token":"tk1"}`)); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if got := gw.worldFor(conn.id); got == nil || got.ID != created.PID.ID {
		t.Fatalf("worldFor(conn) = %v, want %v", got, created.PID)
	}
	// 未知连接 → 默认世界。
	if got := gw.worldFor("nope"); got == nil || got.ID != defaultWorld.ID {
		t.Fatalf("worldFor(default) = %v, want %v", got, defaultWorld)
	}
}
