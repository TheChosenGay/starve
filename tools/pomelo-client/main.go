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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

func main() {
	addr := flag.String("addr", "ws://localhost:8081/ws", "网关 WS 地址")
	uid := flag.String("uid", "42", "用户 ID（登录 token = u<uid>）")
	move := flag.String("move", "", "移动向量，如 \"1,0\"（配合 -interval 周期性发送）")
	gather := flag.Int("gather", 0, "周期性采集的目标实体 ID（0 不发）")
	attack := flag.Int("attack", 0, "周期性攻击的目标实体 ID（0 不发）")
	save := flag.Bool("save", false, "登录后发一次 game.save 请求")
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

	// 2.5 存档（可选）：同步读响应
	if *save {
		writeMessage(conn, pomelo.MsgRequest, 2, proto.RouteSave, nil)
		saveResp := readMessage(conn)
		var sr proto.SaveResponse
		if err := pb.Unmarshal(saveResp.Data, &sr); err != nil {
			log.Fatalf("parse save resp: %v", err)
		}
		fmt.Printf("存档: success=%v\n", sr.Success)
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
			if *gather != 0 {
				data, err := pb.Marshal(&proto.PlayerGather{TargetEntity: uint64(*gather)})
				must(err)
				writeMessage(conn, pomelo.MsgNotify, 0, proto.RouteGather, data)
				fmt.Printf("采集 目标实体 %d\n", *gather)
			}
			if *attack != 0 {
				data, err := pb.Marshal(&proto.PlayerAttack{TargetEntity: uint64(*attack)})
				must(err)
				writeMessage(conn, pomelo.MsgNotify, 0, proto.RouteAttack, data)
				fmt.Printf("攻击 目标实体 %d\n", *attack)
			}
		case <-deadline:
			fmt.Println("结束")
			return
		}
	}
}

func printPush(m *pomelo.Message) {
	switch m.Route {
	case proto.RouteSnapshot:
		var snap game.Snapshot
		if err := pb.Unmarshal(m.Data, &snap); err != nil {
			return
		}
		fmt.Printf("全量快照: %d 实体, 昼夜 phase=%d light=%.2f\n",
			len(snap.Entities), snap.DayCycle.GetPhase(), snap.DayCycle.GetLight())
		for _, es := range snap.Entities {
			fmt.Printf("  实体 %d [%s]\n", es.EntityId, compList(es))
		}
	case proto.RouteSnapshotDelta:
		var delta game.SnapshotDelta
		if err := pb.Unmarshal(m.Data, &delta); err != nil {
			return
		}
		fmt.Printf("增量: 变更 %d, 移除实体 %v, 移除组件 %d, 昼夜 phase=%d light=%.2f\n",
			len(delta.Entities), delta.RemovedEntities, len(delta.RemovedComponents),
			delta.DayCycle.GetPhase(), delta.DayCycle.GetLight())
		for _, es := range delta.Entities {
			fmt.Printf("  实体 %d [%s]\n", es.EntityId, compList(es))
		}
		for _, rc := range delta.RemovedComponents {
			fmt.Printf("  实体 %d 移除组件 [%s]\n", rc.EntityId, strings.Join(rc.Components, ","))
		}
	default:
		fmt.Printf("推送: route=%s data=%v\n", m.Route, m.Data)
	}
}

func compList(es *game.EntityState) string {
	parts := make([]string, 0, len(es.Components))
	for _, cs := range es.Components {
		parts = append(parts, cs.Component+"="+compValue(cs))
	}
	return strings.Join(parts, " ")
}

// compValue 解码常见组件值（客户端调试用；未知组件显示 ?）。
func compValue(cs *game.ComponentState) string {
	switch cs.Component {
	case "Position":
		var v game.Position
		if pb.Unmarshal(cs.Data, &v) == nil {
			return fmt.Sprintf("(%d,%d)", v.X, v.Y)
		}
	case "Health":
		var v game.Health
		if pb.Unmarshal(cs.Data, &v) == nil {
			return fmt.Sprintf("%d/%d", v.Cur, v.Max)
		}
	case "Hunger":
		var v game.Hunger
		if pb.Unmarshal(cs.Data, &v) == nil {
			return fmt.Sprintf("%d(r%d)", v.Level, v.Rate)
		}
	case "Growable":
		var v game.Growable
		if pb.Unmarshal(cs.Data, &v) == nil {
			return fmt.Sprintf("stage%d tick%d", v.Stage, v.Ticks)
		}
	case "Dead":
		var v game.Dead
		if pb.Unmarshal(cs.Data, &v) == nil {
			return "dead:" + v.Reason
		}
	case "Player":
		var v game.Player
		if pb.Unmarshal(cs.Data, &v) == nil {
			return "uid=" + v.Uid
		}
	case "Gatherable":
		var v game.Gatherable
		if pb.Unmarshal(cs.Data, &v) == nil {
			return fmt.Sprintf("%s x%d", v.Kind.String(), v.Count)
		}
	case "Inventory":
		var v game.Inventory
		if pb.Unmarshal(cs.Data, &v) == nil {
			parts := make([]string, 0, len(v.Resources))
			for _, rc := range v.Resources {
				parts = append(parts, fmt.Sprintf("%s:%d", rc.Kind.String(), rc.Count))
			}
			sort.Strings(parts)
			return strings.Join(parts, ",")
		}
	}
	return "?"
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
