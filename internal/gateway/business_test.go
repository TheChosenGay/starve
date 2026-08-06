package gateway

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/TheChosenGay/combet"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/actor"
	"starve/internal/game/components"
	"starve/internal/game/world"
	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
)

type fakeConn struct {
	id      string
	mu      sync.Mutex
	written [][]byte
}

func (f *fakeConn) ID() string   { return f.id }
func (f *fakeConn) Addr() string { return "fake" }
func (f *fakeConn) Write(_ context.Context, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, data)
	return nil
}

func (f *fakeConn) lastPacket(t *testing.T) *pomelo.Packet {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.written) == 0 {
		t.Fatal("no writes")
	}
	packets, err := pomelo.DecodePackets(f.written[len(f.written)-1])
	if err != nil || len(packets) != 1 {
		t.Fatalf("decode packet: %v, %v", packets, err)
	}
	return packets[0]
}

func (f *fakeConn) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.written)
}

func newTestGateway(t *testing.T) (*comet.Core, *actor.Engine, *actor.PID, *world.WorldActor) {
	t.Helper()
	engine := actor.NewEngine(actor.Config{})
	wa := world.NewWorldActor(world.WorldConfig{BroadcastPositions: true})
	worldPID := engine.Spawn(func() actor.IActor { return wa }, "world", "room-1")

	gw := NewGateway(engine, worldPID)
	core := comet.NewCore(comet.ServerConfig{
		Business: gw,
		Scheme:   pomelo.NewScheme(),
	})
	gw.AttachCore(core)
	wa.SetPushSink(gw.HandlePush)
	t.Cleanup(engine.Shutdown)
	return core, engine, worldPID, wa
}

func sendDispatch(t *testing.T, core *comet.Core, conn *fakeConn, pktType byte, body []byte) {
	t.Helper()
	wire, err := pomelo.EncodePacket(pktType, body)
	if err != nil {
		t.Fatal(err)
	}
	core.Dispatch(context.Background(), conn, wire)
}

func TestGatewayHandshakeLoginMove(t *testing.T) {
	core, engine, worldPID, _ := newTestGateway(t)
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)

	// 1. 握手 → 回握手包（JSON code 200）
	sendDispatch(t, core, conn, pomelo.PacketHandshake, []byte(`{"version":"1"}`))
	pkt := conn.lastPacket(t)
	if pkt.Type != pomelo.PacketHandshake || !bytes.Contains(pkt.Data, []byte(`"code":200`)) {
		t.Fatalf("handshake reply = %v", pkt)
	}

	// 2. 握手 ack
	sendDispatch(t, core, conn, pomelo.PacketHandshakeAck, nil)

	// 3. login（request mid=1，route=gate.login）→ 响应 success + 实体 ID
	loginReq, _ := pb.Marshal(&proto.LoginRequest{Token: "u42"})
	loginMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgRequest, ID: 1, Route: proto.RouteLogin, Data: loginReq})
	sendDispatch(t, core, conn, pomelo.PacketData, loginMsg)

	pkt = conn.lastPacket(t)
	if pkt.Type != pomelo.PacketData {
		t.Fatalf("login reply type = %d", pkt.Type)
	}
	respMsg, err := pomelo.DecodeMessage(pkt.Data)
	if err != nil {
		t.Fatal(err)
	}
	if respMsg.Type != pomelo.MsgResponse || respMsg.ID != 1 {
		t.Fatalf("resp = %+v", respMsg)
	}
	var lr proto.LoginResponse
	if err := pb.Unmarshal(respMsg.Data, &lr); err != nil {
		t.Fatal(err)
	}
	if !lr.Success || lr.UserId != "42" || lr.EntityId != 1 {
		t.Fatalf("login = %+v", &lr)
	}

	// 4. 移动（notify，route=world.player.move）→ 世界 actor 命令缓冲 → tick 生效
	mvData, _ := pb.Marshal(&proto.PlayerMove{Dx: 3, Dy: 4})
	mvMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteMove, Data: mvData})
	sendDispatch(t, core, conn, pomelo.PacketData, mvMsg)
	engine.Send(worldPID, world.Tick{})

	// 验证位置变为 (3,4)
	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := engine.Request(worldPID, world.QueryPosition{Entity: 1}, time.Second)
		v, err := resp.Wait()
		if err == nil {
			if pos, ok := v.(components.Position); ok && pos.X == 3 && pos.Y == 4 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("position not updated: %v", v)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestGatewayKickOldConnection(t *testing.T) {
	core, _, _, _ := newTestGateway(t)
	conn1 := &fakeConn{id: "c1"}
	conn2 := &fakeConn{id: "c2"}
	core.ConnManager().Push(conn1)
	core.ConnManager().Push(conn2)

	login := func(conn *fakeConn) {
		sendDispatch(t, core, conn, pomelo.PacketHandshake, []byte(`{}`))
		sendDispatch(t, core, conn, pomelo.PacketHandshakeAck, nil)
		req, _ := pb.Marshal(&proto.LoginRequest{Token: "u42"})
		msg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgRequest, ID: 1, Route: proto.RouteLogin, Data: req})
		sendDispatch(t, core, conn, pomelo.PacketData, msg)
	}
	login(conn1)
	login(conn2) // 同 UID 再次登录 → conn1 被踢

	pkt := conn1.lastPacket(t)
	if pkt.Type != pomelo.PacketKick {
		t.Fatalf("kick type = %d", pkt.Type)
	}
}

func TestGatewayUnknownRouteIgnored(t *testing.T) {
	core, _, _, _ := newTestGateway(t)
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	before := conn.writeCount()
	msg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: "nope.nope", Data: nil})
	sendDispatch(t, core, conn, pomelo.PacketData, msg)
	if conn.writeCount() != before {
		t.Fatal("unknown route should be ignored")
	}
}

// loginConn 完成握手 → ack → login（测试辅助）。
func loginConn(t *testing.T, core *comet.Core, conn *fakeConn, token string) {
	t.Helper()
	sendDispatch(t, core, conn, pomelo.PacketHandshake, []byte(`{}`))
	sendDispatch(t, core, conn, pomelo.PacketHandshakeAck, nil)
	req, _ := pb.Marshal(&proto.LoginRequest{Token: token})
	msg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgRequest, ID: 1, Route: proto.RouteLogin, Data: req})
	sendDispatch(t, core, conn, pomelo.PacketData, msg)
}

// TestGatewayPositionPush：世界 tick 后位置经 outbox → 网关 → 客户端（闭环推送）。
func TestGatewayPositionPush(t *testing.T) {
	core, engine, worldPID, _ := newTestGateway(t)
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	loginConn(t, core, conn, "u42")

	// 移动（notify）→ tick → 世界广播 MovePush
	mvData, _ := pb.Marshal(&proto.PlayerMove{Dx: 3, Dy: 4})
	mvMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteMove, Data: mvData})
	sendDispatch(t, core, conn, pomelo.PacketData, mvMsg)
	engine.Send(worldPID, world.Tick{})

	// 等客户端收到 MovePush (entity=1, x=3, y=4)
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn.mu.Lock()
		n := len(conn.written)
		last := conn.written[n-1]
		conn.mu.Unlock()
		if packets, err := pomelo.DecodePackets(last); err == nil && len(packets) == 1 && packets[0].Type == pomelo.PacketData {
			if m, err := pomelo.DecodeMessage(packets[0].Data); err == nil && m.Type == pomelo.MsgPush && m.Route == proto.RouteMove {
				var push proto.MovePush
				if pb.Unmarshal(m.Data, &push) == nil && push.X == 3 && push.Y == 4 {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no MovePush received")
		}
		time.Sleep(time.Millisecond)
	}
}
