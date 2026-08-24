// 登录真实网关，核对 view_radius / view_preload 与快照实体距离。
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gorilla/websocket"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/devjwt"
	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

func main() {
	addr := "ws://localhost:8081/ws"
	if v := os.Getenv("GATE_WS_URL"); v != "" {
		addr = v
	}
	conn, _, err := websocket.DefaultDialer.Dial(addr, nil)
	if err != nil {
		log.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()

	writePacket(conn, pomelo.PacketHandshake, []byte(`{"version":"0.0.1"}`))
	_ = readPacket(conn)
	writePacket(conn, pomelo.PacketHandshakeAck, nil)

	loginReq, _ := pb.Marshal(&proto.LoginRequest{Token: devjwt.Mint("preload-check")})
	writeMessage(conn, pomelo.MsgRequest, 1, proto.RouteLogin, loginReq)
	resp := readMessage(conn)
	var lr proto.LoginResponse
	if err := pb.Unmarshal(resp.Data, &lr); err != nil || !lr.Success {
		log.Fatalf("login: %+v err=%v", &lr, err)
	}
	fmt.Printf("登录 uid=%s entity=%d\n", lr.UserId, lr.EntityId)

	deadline := time.Now().Add(3 * time.Second)
	var snap *game.Snapshot
	var cfg *game.GameConfig
	for snap == nil || cfg == nil {
		if time.Now().After(deadline) {
			log.Fatalf("超时: snap=%v cfg=%v", snap != nil, cfg != nil)
		}
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		pkt := readPacket(conn)
		if pkt.Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(pkt.Data)
		if err != nil || m.Type != pomelo.MsgPush {
			continue
		}
		switch m.Route {
		case proto.RouteSnapshot:
			var s game.Snapshot
			must(pb.Unmarshal(m.Data, &s))
			snap = &s
		case proto.RouteConfig:
			var c game.GameConfig
			must(pb.Unmarshal(m.Data, &c))
			cfg = &c
		}
	}

	sendR := cfg.ViewRadiusMax + cfg.ViewPreload
	fmt.Printf("契约 view_radius=%d..%d view_preload=%d → 下发半径=%d\n",
		cfg.ViewRadius, cfg.ViewRadiusMax, cfg.ViewPreload, sendR)
	if cfg.ViewRadius != 24 || cfg.ViewRadiusMax != 32 || cfg.ViewPreload != 8 {
		log.Fatalf("期望生产默认 24..32+8，得到 %d..%d+%d", cfg.ViewRadius, cfg.ViewRadiusMax, cfg.ViewPreload)
	}

	ox, oy, ok := entityPos(snap, lr.EntityId)
	if !ok {
		log.Fatal("快照里没有自己的 Position")
	}
	fmt.Printf("自己位置 (%d,%d)，快照实体 %d\n", ox, oy, len(snap.Entities))

	camera, ring, beyond := 0, 0, 0
	fmt.Println("预加载圈内实体（相机外、下发半径内）：")
	for _, es := range snap.Entities {
		x, y, has := entityPosFromState(es)
		if !has {
			continue
		}
		d := cheb(x-ox, y-oy)
		switch {
		case d <= int(cfg.ViewRadiusMax):
			camera++
		case d <= int(sendR):
			ring++
			fmt.Printf("  实体 %d  (%d,%d)  cheb=%d\n", es.EntityId, x, y, d)
		default:
			beyond++
			fmt.Printf("  越界 实体 %d  (%d,%d)  cheb=%d\n", es.EntityId, x, y, d)
		}
	}
	fmt.Printf("分档: 拉远上限内 %d  预加载圈 %d  越界 %d\n", camera, ring, beyond)
	if beyond > 0 {
		log.Fatalf("失败：有 %d 个实体超过 view_radius_max+view_preload", beyond)
	}
	if ring == 0 {
		log.Fatalf("失败：预加载圈是空的，无法确认缓冲是否生效（镜头内=%d）", camera)
	}
	fmt.Println("预加载正常：圈外没有实体，圈内已有缓冲实体")
}

func cheb(dx, dy int) int {
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return dx
	}
	return dy
}

func entityPos(snap *game.Snapshot, id uint64) (int, int, bool) {
	for _, es := range snap.Entities {
		if es.EntityId == id {
			return entityPosFromState(es)
		}
	}
	return 0, 0, false
}

func entityPosFromState(es *game.EntityState) (int, int, bool) {
	for _, cs := range es.Components {
		if cs.Component != "Position" {
			continue
		}
		var p game.Position
		if pb.Unmarshal(cs.Data, &p) != nil {
			return 0, 0, false
		}
		return int(p.X), int(p.Y), true
	}
	return 0, 0, false
}

func writePacket(conn *websocket.Conn, t byte, body []byte) {
	wire, err := pomelo.EncodePacket(t, body)
	must(err)
	must(conn.WriteMessage(websocket.BinaryMessage, wire))
}

func readPacket(conn *websocket.Conn) *pomelo.Packet {
	_, raw, err := conn.ReadMessage()
	must(err)
	packets, err := pomelo.DecodePackets(raw)
	must(err)
	if len(packets) != 1 {
		log.Fatalf("expected 1 packet, got %d", len(packets))
	}
	return packets[0]
}

func writeMessage(conn *websocket.Conn, typ byte, mid uint64, route string, data []byte) {
	wire, err := pomelo.EncodeMessage(&pomelo.Message{Type: typ, ID: mid, Route: route, Data: data})
	must(err)
	writePacket(conn, pomelo.PacketData, wire)
}

func readMessage(conn *websocket.Conn) *pomelo.Message {
	pkt := readPacket(conn)
	if pkt.Type != pomelo.PacketData {
		log.Fatalf("expected data packet, got type %d", pkt.Type)
	}
	m, err := pomelo.DecodeMessage(pkt.Data)
	must(err)
	return m
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
