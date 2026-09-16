// throwprobe 验证投掷的**端到端链路**（真实 ws 连接）。
//
// 最小联调：登录 → 投掷 → 观察背包是否减少、是否出现飞行物、落地是否爆炸。
//
// 用法：go run ./tools/throwprobe -uid throwtest1 -to "68,64"
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/gorilla/websocket"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/devjwt"
	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

func writePacket(c *websocket.Conn, t byte, data []byte) {
	b, err := pomelo.EncodePacket(t, data)
	if err != nil {
		log.Fatalf("encode packet: %v", err)
	}
	if err := c.WriteMessage(websocket.BinaryMessage, b); err != nil {
		log.Fatalf("write: %v", err)
	}
}

func readPacket(c *websocket.Conn) *pomelo.Packet {
	_, b, err := c.ReadMessage()
	if err != nil {
		log.Fatalf("read: %v", err)
	}
	pkts, err := pomelo.DecodePackets(b)
	if err != nil || len(pkts) == 0 {
		return nil
	}
	return pkts[0]
}

func writeMessage(c *websocket.Conn, typ byte, mid uint64, route string, data []byte) {
	b, err := pomelo.EncodeMessage(&pomelo.Message{Type: typ, ID: mid, Route: route, Data: data})
	if err != nil {
		log.Fatalf("encode message: %v", err)
	}
	writePacket(c, pomelo.PacketData, b)
}

func main() {
	addr := flag.String("addr", "ws://localhost:8081/ws", "网关地址")
	uid := flag.String("uid", "throwtest1", "用户 ID")
	to := flag.String("to", "68,64", "投掷落点 \"x,y\"")
	flag.Parse()

	conn, _, err := websocket.DefaultDialer.Dial(*addr, nil)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	writePacket(conn, pomelo.PacketHandshake, []byte(`{"version":"0.0.1"}`))
	readPacket(conn)
	writePacket(conn, pomelo.PacketHandshakeAck, nil)

	loginReq, _ := pb.Marshal(&proto.LoginRequest{Token: devjwt.Mint(*uid)})
	writeMessage(conn, pomelo.MsgRequest, 1, proto.RouteLogin, loginReq)

	var entityID uint64
	var posX, posY float64
	bombsBefore := -1
	// **两阶段**等待：先拿到 entityID，再读快照。
	//
	// 为什么不能在一次循环里同时等：登录瞬间服务端就会推全量快照，
	// 它可能**先于** login 响应到达。此时 entityID 还是 0，
	// 遍历快照一个实体都匹配不上 → Position/Inventory 全读不到，
	// 表现为"玩家在 (0,0)、背包 -1"，看起来像投掷没生效（实测踩过）。
	waitUntil := time.Now().Add(8 * time.Second)
	for entityID == 0 && time.Now().Before(waitUntil) {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		p := readPacket(conn)
		if p == nil || p.Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(p.Data)
		if err != nil {
			continue
		}
		// 登录响应是 **MsgResponse 且不带 Route**（Route 只在 notify/push 上有值），
		// 所以不能按 `m.Route == RouteLogin` 匹配——那样永远匹配不到（实测踩过）。
		if m.Type == pomelo.MsgResponse {
			var lr proto.LoginResponse
			if pb.Unmarshal(m.Data, &lr) == nil && lr.EntityId != 0 {
				entityID = lr.EntityId
				fmt.Printf("登录 entity=%d success=%v\n", entityID, lr.Success)
			}
		}
	}
	if entityID == 0 {
		log.Fatal("未能登录（未收到 LoginResponse）")
	}

	// 第二阶段：等快照（可能已经在上一阶段的缓冲里，也可能稍后到）
	waitUntil = time.Now().Add(8 * time.Second)
	for time.Now().Before(waitUntil) {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		p := readPacket(conn)
		if p == nil || p.Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(p.Data)
		if err != nil {
			continue
		}
		if m.Type == pomelo.MsgResponse && m.Route == proto.RouteLogin {
			var lr proto.LoginResponse
			if pb.Unmarshal(m.Data, &lr) == nil {
				entityID = lr.EntityId
				fmt.Printf("登录 entity=%d\n", entityID)
			}
			// **不要**在这里 break：全量快照是随后单独推送的，
			// 提前跳出会导致 Position/Inventory 都读不到（实测踩过：
			// 位置显示 (0,0)、背包 -1，看起来像"投掷没生效"，其实是没读到）。
			continue
		}
		if m.Type == pomelo.MsgPush && m.Route == proto.RouteSnapshot {
			var snap game.Snapshot
			if pb.Unmarshal(m.Data, &snap) != nil {
				continue
			}
			for _, es := range snap.Entities {
				if es.EntityId != entityID {
					continue
				}
				for _, c := range es.Components {
					switch c.Component {
					case "Position":
						var v game.Position
						if pb.Unmarshal(c.Data, &v) == nil {
							posX, posY = float64(v.X), float64(v.Y)
						}
					case "Inventory":
						var v game.Inventory
						if pb.Unmarshal(c.Data, &v) == nil {
							bombsBefore = 0
							for _, s := range v.Items {
								if s != nil && s.Kind == game.ItemKind_ITEM_KIND_BOMB {
									bombsBefore += int(s.Count)
								}
							}
						}
					}
				}
			}
			if bombsBefore >= 0 {
				break // 快照里已拿到背包
			}
		}
	}
	fmt.Printf("玩家 (%.0f,%.0f) 背包炸弹=%d\n", posX, posY, bombsBefore)

	// 解析落点
	var tx, ty float64
	if _, err := fmt.Sscanf(*to, "%f,%f", &tx, &ty); err != nil {
		log.Fatalf("bad -to %q: %v", *to, err)
	}
	dist := (func(dx, dy float64) float64 {
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		return (func() float64 {
			if dx > dy {
				return dx
			}
			return dy
		})()
	})(tx-posX, ty-posY)
	fmt.Printf("投掷 → (%.0f,%.0f) 距离 %.0f 格\n", tx, ty, dist)

	// 发送投掷命令（ThrownEntity=0：服务端从背包取炸弹）
	data, _ := pb.Marshal(&proto.PlayerThrow{ToX: float32(tx), ToY: float32(ty)})
	writeMessage(conn, pomelo.MsgNotify, 0, proto.RouteThrow, data)
	fmt.Println("已发送 PlayerThrow")

	// 观察 3 秒：追踪飞行物（有 Thrown 的实体）+ 爆炸事件
	deadline := time.Now().Add(5 * time.Second) // 飞行 20 tick = 1 秒，留足余量看落地
	flyingSeen := 0
	flyingDetail := ""
	blasts := 0
	inventoryAfter := bombsBefore
	for time.Now().Before(deadline) {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		p := readPacket(conn)
		if p == nil || p.Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(p.Data)
		if err != nil || m.Type != pomelo.MsgPush {
			continue
		}
		switch m.Route {
		case proto.RouteSnapshotDelta:
			var d game.SnapshotDelta
			if pb.Unmarshal(m.Data, &d) != nil {
				continue
			}
			for _, es := range d.Entities {
				for _, c := range es.Components {
					if c.Component == "Thrown" {
						var th game.Thrown
						if pb.Unmarshal(c.Data, &th) == nil {
							flyingSeen++
							flyingDetail = fmt.Sprintf("实体%d 飞行 %d/%d tick 落点(%.0f,%.0f) 投掷者=%d",
								es.EntityId, th.Elapsed, th.FlightTicks, th.ToX, th.ToY, th.Thrower)
						}
					}
				}
				if es.EntityId == entityID {
					for _, c := range es.Components {
						if c.Component == "Inventory" {
							var v game.Inventory
							if pb.Unmarshal(c.Data, &v) == nil {
								inventoryAfter = 0
								for _, s := range v.Items {
									if s != nil && s.Kind == game.ItemKind_ITEM_KIND_BOMB {
										inventoryAfter += int(s.Count)
									}
								}
							}
						}
					}
				}
			}
			for _, ev := range d.Events {
				if b := ev.GetBlast(); b != nil {
					blasts++
					fmt.Printf("  ★ 爆炸事件：中心(%.0f,%.0f) 半径%.1f 来源=%d\n",
						b.X, b.Y, b.Radius, b.SourceEntity)
				}
			}
		}
	}

	fmt.Println("--- 结果 ---")
	// 注意：落地后 Thrown 组件被移除，所以这个次数通常很小（1~3）。
	// 它是"曾观察到飞行"的证据，不是飞行时长。
	fmt.Printf("飞行物快照次数 = %d\n", flyingSeen)
	if flyingDetail != "" {
		fmt.Printf("  最后一次：%s\n", flyingDetail)
	}
	fmt.Printf("背包炸弹 %d → %d\n", bombsBefore, inventoryAfter)
	fmt.Printf("爆炸事件 = %d\n", blasts)
	ok := true
	if inventoryAfter >= bombsBefore && bombsBefore > 0 {
		fmt.Println("✗ 背包没有减少（投掷没被受理？）")
		ok = false
	}
	if flyingSeen == 0 && blasts == 0 {
		fmt.Println("✗ 既没看到飞行物也没看到爆炸（意图可能被拒）")
		ok = false
	}
	if ok {
		fmt.Println("✓ 投掷链路打通")
	}
	_ = json.Marshal
}
