// pomelo-client 是 M4 最小闭环的测试客户端（命令行）：
// 连接 → 握手 → ack → 登录 → 移动 → 打印收到的位置推送。
//
// 用法：
//
//	go run ./tools/pomelo-client -uid 42                                  # 只收推送
//	go run ./tools/pomelo-client -uid 43 -move 1,0 -interval 500ms        # 每 500ms 移动一次
//
// 双客户端验收：开两个客户端，一个移动，另一个应看到对方实体位置变化。
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
)

func main() {
	addr := flag.String("addr", "ws://localhost:8081/ws", "网关 WS 地址")
	uid := flag.String("uid", "42", "用户 ID（登录 token = u<uid>）")
	move := flag.String("move", "", "移动向量，如 \"1,0\"（配合 -interval 周期性发送）")
	interval := flag.Duration("interval", time.Second, "移动发送间隔")
	duration := flag.Duration("duration", 10*time.Second, "运行时长")
	flag.Parse()

	conn, _, err := websocket.DefaultDialer.Dial(*addr, nil)
	if err != nil {
		log.Fatalf("dial %s: %v", *addr, err)
	}
	defer conn.Close()
	fmt.Printf("已连接 %s\n", *addr)

	// 1. 握手 → 响应 → ack
	writePacket(conn, pomelo.PacketHandshake, []byte(`{"version":"0.0.1"}`))
	hs := readPacket(conn)
	fmt.Printf("握手响应: %s\n", hs.Data)
	writePacket(conn, pomelo.PacketHandshakeAck, nil)

	// 2. 登录
	loginReq, err := pb.Marshal(&proto.LoginRequest{Token: "u" + *uid})
	must(err)
	writeMessage(conn, pomelo.MsgRequest, 1, proto.RouteLogin, loginReq)
	resp := readMessage(conn)
	var lr proto.LoginResponse
	if err := pb.Unmarshal(resp.Data, &lr); err != nil {
		log.Fatalf("parse login resp: %v", err)
	}
	fmt.Printf("登录: success=%v uid=%s entity=%d msg=%s\n", lr.Success, lr.UserId, lr.EntityId, lr.Message)
	if !lr.Success {
		os.Exit(1)
	}

	// 3. 收推送（后台打印）
	go func() {
		for {
			pkt := readPacket(conn)
			switch pkt.Type {
			case pomelo.PacketKick:
				fmt.Printf("被服务器踢出: %s\n", pkt.Data)
				os.Exit(1)
			case pomelo.PacketData:
				m, err := pomelo.DecodeMessage(pkt.Data)
				if err != nil {
					continue
				}
				if m.Type == pomelo.MsgPush {
					printPush(m)
				}
			}
		}
	}()

	// 4. 移动（可选）
	var dx, dy int
	if *move != "" {
		parts := strings.Split(*move, ",")
		dx, _ = strconv.Atoi(parts[0])
		dy, _ = strconv.Atoi(parts[1])
	}
	tick := time.NewTicker(*interval)
	defer tick.Stop()
	deadline := time.After(*duration)
	for {
		select {
		case <-tick.C:
			if *move != "" {
				data, err := pb.Marshal(&proto.PlayerMove{Dx: int32(dx), Dy: int32(dy)})
				must(err)
				writeMessage(conn, pomelo.MsgNotify, 0, proto.RouteMove, data)
				fmt.Printf("移动 (%d,%d)\n", dx, dy)
			}
		case <-deadline:
			fmt.Println("结束")
			return
		}
	}
}

func printPush(m *pomelo.Message) {
	switch m.Route {
	case proto.RouteMove:
		var push proto.MovePush
		if err := pb.Unmarshal(m.Data, &push); err != nil {
			return
		}
		fmt.Printf("推送: 实体 %d @ (%d,%d)\n", push.EntityId, push.X, push.Y)
	default:
		fmt.Printf("推送: route=%s data=%v\n", m.Route, m.Data)
	}
}

func writePacket(conn *websocket.Conn, t byte, body []byte) {
	wire, err := pomelo.EncodePacket(t, body)
	must(err)
	if err := conn.WriteMessage(websocket.BinaryMessage, wire); err != nil {
		log.Fatalf("write packet: %v", err)
	}
}

func readPacket(conn *websocket.Conn) *pomelo.Packet {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		log.Fatalf("read packet: %v", err)
	}
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
		log.Fatalf("%v", err)
	}
}
