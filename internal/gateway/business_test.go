package gateway

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TheChosenGay/combet"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/actor"
	"starve/internal/devjwt"
	"starve/internal/game/components"
	"starve/internal/game/world"
	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
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
	core, engine, pid, wa, _ := newTestGatewayFull(t, world.WorldConfig{})
	return core, engine, pid, wa
}

func newTestGatewayFull(t *testing.T, cfg world.WorldConfig) (*comet.Core, *actor.Engine, *actor.PID, *world.WorldActor, *Gateway) {
	t.Helper()
	engine := actor.NewEngine(actor.Config{})
	wa := world.NewWorldActor(cfg)
	worldPID := engine.Spawn(func() actor.IActor { return wa }, "world", "room-1")

	gw := NewGateway(engine, worldPID)
	core := comet.NewCore(comet.ServerConfig{
		Business: gw,
		Scheme:   pomelo.NewScheme(),
	})
	gw.AttachCore(core)
	wa.SetPushSink(gw.HandlePush)
	t.Cleanup(engine.Shutdown)
	return core, engine, worldPID, wa, gw
}

func newTestGatewayCfg(t *testing.T, cfg world.WorldConfig) (*comet.Core, *actor.Engine, *actor.PID, *world.WorldActor) {
	t.Helper()
	core, engine, pid, wa, _ := newTestGatewayFull(t, cfg)
	return core, engine, pid, wa
}

func sendDispatch(t *testing.T, core *comet.Core, conn *fakeConn, pktType byte, body []byte) {
	t.Helper()
	wire, err := pomelo.EncodePacket(pktType, body)
	if err != nil {
		t.Fatal(err)
	}
	core.Dispatch(context.Background(), conn, wire)
}

// findResponse 在 conn.written 中从后往前找指定 mid 的 response 消息。
func findResponse(t *testing.T, conn *fakeConn, mid uint64) *pomelo.Message {
	t.Helper()
	conn.mu.Lock()
	writes := append([][]byte(nil), conn.written...)
	conn.mu.Unlock()
	for i := len(writes) - 1; i >= 0; i-- {
		packets, err := pomelo.DecodePackets(writes[i])
		if err != nil || len(packets) != 1 || packets[0].Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(packets[0].Data)
		if err != nil || m.Type != pomelo.MsgResponse || m.ID != mid {
			continue
		}
		return m
	}
	return nil
}

// findPush 在 conn.written 中从后往前找指定 route 的 push 消息。
func findPush(t *testing.T, conn *fakeConn, route string) *pomelo.Message {
	t.Helper()
	conn.mu.Lock()
	writes := append([][]byte(nil), conn.written...)
	conn.mu.Unlock()
	for i := len(writes) - 1; i >= 0; i-- {
		packets, err := pomelo.DecodePackets(writes[i])
		if err != nil || len(packets) != 1 || packets[0].Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(packets[0].Data)
		if err != nil || m.Type != pomelo.MsgPush || m.Route != route {
			continue
		}
		return m
	}
	return nil
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
	loginReq, _ := pb.Marshal(&proto.LoginRequest{Token: devjwt.Mint("42")})
	loginMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgRequest, ID: 1, Route: proto.RouteLogin, Data: loginReq})
	sendDispatch(t, core, conn, pomelo.PacketData, loginMsg)

	respMsg := findResponse(t, conn, 1)
	if respMsg == nil {
		t.Fatal("no login response")
	}
	if respMsg.Type != pomelo.MsgResponse {
		t.Fatalf("resp = %+v", respMsg)
	}
	var lr proto.LoginResponse
	if err := pb.Unmarshal(respMsg.Data, &lr); err != nil {
		t.Fatal(err)
	}
	if !lr.Success || lr.UserId != "42" || lr.EntityId != 1 {
		t.Fatalf("login = %+v", &lr)
	}

	// 4. 移动（notify，route=world.player.move）→ 世界 actor 命令缓冲 → tick 制推进
	//    客户端发的 (3,4) 被服务端按方向意图收敛为 (1,1)
	mvData, _ := pb.Marshal(&proto.PlayerMove{Dx: 3, Dy: 4})
	mvMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteMove, Data: mvData})
	sendDispatch(t, core, conn, pomelo.PacketData, mvMsg)

	// 验证位置随 tick 推进到 (1,1)
	deadline := time.Now().Add(2 * time.Second)
	for {
		engine.Send(worldPID, world.Tick{})
		resp := engine.Request(worldPID, world.QueryPosition{Entity: 1}, time.Second)
		v, err := resp.Wait()
		if err == nil {
			if pos, ok := v.(components.Position); ok && pos.X == 1 && pos.Y == 1 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("position not updated: %v", v)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestGatewayGather：采集路由 → 世界命令 → 增量快照携带背包变更。
func TestGatewayGather(t *testing.T) {
	// 资源配置：一个浆果丛在 (0,1)，玩家出生 (0,0) 在范围内
	resPath := filepath.Join(t.TempDir(), "resources.json")
	if err := os.WriteFile(resPath, []byte(`[{"kind":"berry","x":0,"y":1,"action":"pick","work":3}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	core, engine, worldPID, _ := newTestGatewayCfg(t, world.WorldConfig{ResourcesPath: resPath})
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	loginConn(t, core, conn, "u42")

	// 资源先 seed（实体 1），玩家登录后是实体 2
	grData, _ := pb.Marshal(&proto.PlayerGather{TargetEntity: 1})
	grMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteGather, Data: grData})
	sendDispatch(t, core, conn, pomelo.PacketData, grMsg)
	engine.Send(worldPID, world.Tick{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if m := findPush(t, conn, proto.RouteSnapshotDelta); m != nil {
			var delta game.SnapshotDelta
			if pb.Unmarshal(m.Data, &delta) == nil {
				invOK := false
				bushOK := false
				for _, es := range delta.Entities {
					for _, cs := range es.Components {
						switch cs.Component {
						case "Inventory":
							if es.EntityId == 2 { // 玩家（资源先 seed，玩家后创建）
								var inv game.Inventory
								if pb.Unmarshal(cs.Data, &inv) == nil {
									for _, s := range inv.Items {
										if s.Kind == game.ItemKind_ITEM_KIND_BERRY && s.Count == 1 {
											invOK = true
										}
									}
								}
							}
						case "Pickable":
							if es.EntityId == 1 { // 浆果丛 WorkLeft 3→2
								var w game.WorkTarget
								if pb.Unmarshal(cs.Data, &w) == nil && w.WorkLeft == 2 {
									bushOK = true
								}
							}
						}
					}
				}
				if invOK && bushOK {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no Inventory delta with berry=1 after gather")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestGatewayAutomate：空格自动行为路由 → FindBest 就近选目标 → 世界执行 → 快照携带背包变化。
// 玩家出生 (0,0)，唯一资源浆果丛在 (0,1)（Picker 范围 1 内）→ 自动采集。
func TestGatewayAutomate(t *testing.T) {
	resPath := filepath.Join(t.TempDir(), "resources.json")
	if err := os.WriteFile(resPath, []byte(`[{"kind":"berry","x":0,"y":1,"action":"pick","work":3}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	core, engine, worldPID, _ := newTestGatewayCfg(t, world.WorldConfig{ResourcesPath: resPath})
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	loginConn(t, core, conn, "u42")

	auMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteAutomate, Data: nil})
	sendDispatch(t, core, conn, pomelo.PacketData, auMsg)
	engine.Send(worldPID, world.Tick{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if m := findPush(t, conn, proto.RouteSnapshotDelta); m != nil {
			var delta game.SnapshotDelta
			if pb.Unmarshal(m.Data, &delta) == nil {
				for _, es := range delta.Entities {
					if es.EntityId != 2 { // 玩家（资源先 seed，玩家后创建）
						continue
					}
					for _, cs := range es.Components {
						if cs.Component != "Inventory" {
							continue
						}
						var inv game.Inventory
						if pb.Unmarshal(cs.Data, &inv) == nil {
							for _, s := range inv.Items {
								if s.Kind == game.ItemKind_ITEM_KIND_BERRY && s.Count == 1 {
									return
								}
							}
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("automate 后快照无 berry=1 的背包变更")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestGatewayAttack：攻击路由 → 世界命令 → 增量快照携带 Health 变化。
// 目标用玩家自己的实体（距离 0 合法），验证路由/命令/快照整条链路。
func TestGatewayAttack(t *testing.T) {
	core, engine, worldPID, _ := newTestGateway(t)
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	loginConn(t, core, conn, "u42")

	atData, _ := pb.Marshal(&proto.PlayerAttack{TargetEntity: 1})
	atMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteAttack, Data: atData})
	sendDispatch(t, core, conn, pomelo.PacketData, atMsg)
	engine.Send(worldPID, world.Tick{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		if m := findPush(t, conn, proto.RouteSnapshotDelta); m != nil {
			var delta game.SnapshotDelta
			if pb.Unmarshal(m.Data, &delta) == nil {
				for _, es := range delta.Entities {
					if es.EntityId == 1 {
						for _, cs := range es.Components {
							if cs.Component == "Health" {
								var h game.Health
								if pb.Unmarshal(cs.Data, &h) == nil && h.Cur == 90 { // 默认伤害 10
									return
								}
							}
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no Health delta after attack")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestGatewayUse：使用路由 → 背包消耗 + 饥饿恢复（模板 use_effect）。
func TestGatewayUse(t *testing.T) {
	dir := t.TempDir()
	resPath := filepath.Join(dir, "resources.json")
	tmplPath := filepath.Join(dir, "templates.json")
	if err := os.WriteFile(resPath, []byte(`[{"kind":"berry","x":0,"y":1,"action":"pick","work":3}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmplPath, []byte(`{"berry":{"stack_size":20,"use_effect":{"hunger":8}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	core, engine, worldPID, _ := newTestGatewayCfg(t, world.WorldConfig{
		HungerRate:    1,
		ResourcesPath: resPath,
		TemplatesPath: tmplPath,
	})
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	loginConn(t, core, conn, "u42")

	// 饿 10 tick：饥饿 90；采集（tick 内再扣 1 → 89）；吃（+8 再扣 1）→ 96
	for i := 0; i < 10; i++ {
		engine.Send(worldPID, world.Tick{})
	}
	grData, _ := pb.Marshal(&proto.PlayerGather{TargetEntity: 1})
	grMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteGather, Data: grData})
	sendDispatch(t, core, conn, pomelo.PacketData, grMsg)
	engine.Send(worldPID, world.Tick{})
	useData, _ := pb.Marshal(&proto.PlayerUse{Kind: 1}) // ResourceKind_BERRY
	useMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteUse, Data: useData})
	sendDispatch(t, core, conn, pomelo.PacketData, useMsg)
	engine.Send(worldPID, world.Tick{})

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := engine.Request(worldPID, world.QuerySnapshot{}, time.Second)
		v, err := resp.Wait()
		if err == nil {
			if snap, ok := v.(*game.Snapshot); ok {
				if h, ok := snapHunger(snap, 2); ok && h == 96 {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("使用浆果后饥饿未恢复到 96")
		}
		time.Sleep(time.Millisecond)
	}
}

// snapHunger 从全量快照读某实体 Hunger.Level。
func snapHunger(snap *game.Snapshot, entity uint64) (int32, bool) {
	for _, es := range snap.Entities {
		if es.EntityId != entity {
			continue
		}
		for _, cs := range es.Components {
			if cs.Component == "Hunger" {
				var h game.Hunger
				if pb.Unmarshal(cs.Data, &h) == nil {
					return h.Level, true
				}
			}
		}
	}
	return 0, false
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
		req, _ := pb.Marshal(&proto.LoginRequest{Token: devjwt.Mint("42")})
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

// TestGatewaySweeperDisconnect：连接从 ConnManager 移除（断线）→ sweepOnce
// 通知世界 → 玩家实体挂 Offline（离线保留）。
func TestGatewaySweeperDisconnect(t *testing.T) {
	core, engine, worldPID, _, gw := newTestGatewayFull(t, world.WorldConfig{})
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	loginConn(t, core, conn, "u42")

	core.ConnManager().Pop(conn) // 模拟 ws server 断线清理
	gw.sweepOnce()

	deadline := time.Now().Add(2 * time.Second)
	for {
		resp := engine.Request(worldPID, world.QuerySnapshot{}, time.Second)
		v, err := resp.Wait()
		if err == nil {
			if snap, ok := v.(*game.Snapshot); ok && snapHasComponent(snap, 1, "Offline") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("断线后实体未被标记 Offline")
		}
		time.Sleep(time.Millisecond)
	}
}

// snapHasComponent 判断全量快照里某实体是否带某组件。
func snapHasComponent(snap *game.Snapshot, entity uint64, comp string) bool {
	for _, es := range snap.Entities {
		if es.EntityId != entity {
			continue
		}
		for _, cs := range es.Components {
			if cs.Component == comp {
				return true
			}
		}
	}
	return false
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

// TestGatewaySave：客户端点存档 → 世界保存 → 回复成功。
func TestGatewaySave(t *testing.T) {
	core, _, _, _ := newTestGateway(t)
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	loginConn(t, core, conn, "u42")

	// game.save 请求（mid=2，无 body）
	msg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgRequest, ID: 2, Route: proto.RouteSave, Data: nil})
	sendDispatch(t, core, conn, pomelo.PacketData, msg)

	respMsg := findResponse(t, conn, 2)
	if respMsg == nil {
		t.Fatal("no save response")
	}
	var sr proto.SaveResponse
	if err := pb.Unmarshal(respMsg.Data, &sr); err != nil || !sr.Success {
		t.Fatalf("save resp = %+v, err = %v", &sr, err)
	}
}

// loginConn 完成握手 → ack → login（测试辅助；token 参数视为 uid，按 feeds JWT 签发）。
func loginConn(t *testing.T, core *comet.Core, conn *fakeConn, uid string) {
	t.Helper()
	sendDispatch(t, core, conn, pomelo.PacketHandshake, []byte(`{}`))
	sendDispatch(t, core, conn, pomelo.PacketHandshakeAck, nil)
	req, _ := pb.Marshal(&proto.LoginRequest{Token: devjwt.Mint(uid)})
	msg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgRequest, ID: 1, Route: proto.RouteLogin, Data: req})
	sendDispatch(t, core, conn, pomelo.PacketData, msg)
}

// TestGatewaySnapshotDelta：登录下发全量快照；世界 tick 后增量快照含位置变更。
func TestGatewaySnapshotDelta(t *testing.T) {
	core, engine, worldPID, _ := newTestGateway(t)
	conn := &fakeConn{id: "c1"}
	core.ConnManager().Push(conn)
	loginConn(t, core, conn, "u42")

	// 登录后应收到全量 Snapshot（重建实体表）
	snapMsg := findPush(t, conn, proto.RouteSnapshot)
	if snapMsg == nil {
		t.Fatal("no full snapshot after login")
	}
	var snap game.Snapshot
	if err := pb.Unmarshal(snapMsg.Data, &snap); err != nil || len(snap.Entities) != 1 {
		t.Fatalf("snapshot = %+v, err = %v", &snap, err)
	}
	if cfgMsg := findPush(t, conn, proto.RouteConfig); cfgMsg == nil {
		t.Fatal("登录后应推送 world.config（模板/配方/工作站）")
	}

	// 移动（notify）→ tick → 世界广播 SnapshotDelta 含 Position(1,1)
	mvData, _ := pb.Marshal(&proto.PlayerMove{Dx: 3, Dy: 4})
	mvMsg, _ := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgNotify, Route: proto.RouteMove, Data: mvData})
	sendDispatch(t, core, conn, pomelo.PacketData, mvMsg)

	deadline := time.Now().Add(2 * time.Second)
	for {
		engine.Send(worldPID, world.Tick{})
		if m := findPush(t, conn, proto.RouteSnapshotDelta); m != nil {
			var delta game.SnapshotDelta
			if pb.Unmarshal(m.Data, &delta) == nil {
				for _, es := range delta.Entities {
					if es.EntityId == 1 {
						for _, cs := range es.Components {
							if cs.Component == "Position" {
								var p game.Position
								if pb.Unmarshal(cs.Data, &p) == nil && p.X == 1 && p.Y == 1 {
									return
								}
							}
						}
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no SnapshotDelta with Position(1,1)")
		}
		time.Sleep(time.Millisecond)
	}
}
