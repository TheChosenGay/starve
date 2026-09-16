// snapprobe 测量**移动中玩家**的连续位置下发频率（fix A 的端到端验证）。
//
// 背景：客户端 PositionSmoother 依赖"服务端每 tick 下发连续子格偏移"这一契约。
// 该契约此前未被实现（只在跨格时标脏 Moveable），导致客户端只拿到约 10Hz 的
// 有效位置更新而渲染 60FPS，表现为走动一卡一跳。
//
// 判据：玩家以 10 格/秒 移动、服务端 20Hz tick。若每 tick 都下发，
// 每秒应看到约 20 个**互不相同**的连续位置（10 格/秒 ÷ 20Hz = 每帧 +0.5 格）；
// 若只在跨格时下发，则每秒约 10 个，且大量帧的 Position+Sub 完全不变。
//
// 实测（修复后，uid=45）：
//
//	64.5 → 65.0 → 65.5 → 66.0 → 66.5 → 67.0 …（每帧 +0.5 格，无重复）
//	第 1 秒位置变化 20 次，正好等于 tick 率。
//
// 注意：Position 只在**跨格**时下发（有意为之，避免无谓字段），
// 因此连续位置 = 最近一次已知整格 + 本次 Moveable.SubX。
// 若看到 subX 长期钉在 0.999 且位置不变，那是**实体撞墙卡住**（合法停止），
// 不是下发缺失——用 -v 可打印原始字段确认。
//
// 用法：go run ./tools/snapprobe -uid 42 -duration 8s [-v]
package main

import (
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
	uid := flag.String("uid", "42", "用户 ID")
	duration := flag.Duration("duration", 8*time.Second, "采样时长")
	verbose := flag.Bool("v", false, "打印前若干帧的原始字段（诊断卡住原因时用）")
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
	for {
		p := readPacket(conn)
		if p == nil || p.Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(p.Data)
		if err != nil || m.Type != pomelo.MsgResponse {
			continue
		}
		var lr proto.LoginResponse
		if pb.Unmarshal(m.Data, &lr) == nil {
			entityID = lr.EntityId
			fmt.Printf("登录 entity=%d\n", entityID)
			break
		}
	}

	start := time.Now()
	stop := start.Add(*duration)
	go func() {
		for time.Now().Before(stop) {
			d, _ := pb.Marshal(&proto.PlayerMove{Dx: 1, Dy: 0})
			writeMessage(conn, pomelo.MsgNotify, 0, proto.RouteMove, d)
			time.Sleep(100 * time.Millisecond)
		}
	}()

	routes := map[string]int{}

	entsSeen := map[uint64]int{}
	framesWithMv := 0
	// 统计玩家位置样本：连续位置 = Position.X + Moveable.SubX
	total, changed, same := 0, 0, 0
	var lastAnchor float64
	haveAnchor := false
	var prev float64
	havePrev := false
	seenValues := map[string]struct{}{}
	seconds := 0
	var perSecond []int
	var secChanged int

	for time.Now().Before(stop) {
		conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		p := readPacket(conn)
		if p == nil || p.Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(p.Data)
		if err != nil || m.Type != pomelo.MsgPush {
			continue
		}
		routes[m.Route]++
		// 全量快照里第一次拿到玩家的整格坐标（之后增量基本不再下发 Position）。
		if m.Route == proto.RouteSnapshot {
			var snap game.Snapshot
			if pb.Unmarshal(m.Data, &snap) == nil {
				for _, es := range snap.Entities {
					if es.EntityId != entityID {
						continue
					}
					for _, c := range es.Components {
						if c.Component != "Position" {
							continue
						}
						var v game.Position
						if pb.Unmarshal(c.Data, &v) == nil {
							lastAnchor = float64(v.X)
							haveAnchor = true
						}
					}
				}
			}
			continue
		}
		if m.Route != proto.RouteSnapshotDelta {
			continue
		}
		var delta game.SnapshotDelta
		if pb.Unmarshal(m.Data, &delta) != nil {
			continue
		}
		for _, es := range delta.Entities {
			entsSeen[es.EntityId]++
			if es.EntityId != entityID {
				continue
			}
			framesWithMv++
			var mv *game.Moveable
			var pos *game.Position
			for _, c := range es.Components {
				switch c.Component {
				case "Moveable":
					var v game.Moveable
					if pb.Unmarshal(c.Data, &v) == nil {
						mv = &v
					}
				case "Position":
					var v game.Position
					if pb.Unmarshal(c.Data, &v) == nil {
						pos = &v
					}
				}
			}
			// 只看携带 Moveable 的帧（= 服务端下发了连续位置）。
			// Position 只在跨格时下发（有意为之，避免无谓字段），
			// 所以连续位置要用**最近一次已知的整格** + 本次 Sub。
			if mv == nil {
				continue
			}
			if pos != nil {
				lastAnchor = float64(pos.X)
				haveAnchor = true
			}
			if !haveAnchor {
				continue
			}
			fx := lastAnchor + mv.SubX
			total++
			key := fmt.Sprintf("%.4f", fx)
			seenValues[key] = struct{}{}
			if havePrev {
				if fx == prev {
					same++
				} else {
					changed++
					secChanged++
				}
			}
			if *verbose && (total <= 12 || total%20 == 0) {
				fmt.Printf("  #%d anchor=%.0f subX=%.4f dir=(%d,%d) vel=(%.3f,%.3f) fx=%.4f\n",
					total, lastAnchor, mv.SubX, mv.DirX, mv.DirY, mv.VelX, mv.VelY, fx)
			}
			prev = fx
			havePrev = true

			if int(time.Since(start).Seconds()) > seconds {
				perSecond = append(perSecond, secChanged)
				secChanged = 0
				seconds++
			}
		}
	}

	if total == 0 {
		fmt.Printf("路由统计: %v\n", routes)
		fmt.Printf("增量里出现玩家实体的次数=%d；增量实体总数(去重)=%d\n", framesWithMv, len(entsSeen))
		top := 0
		for _, v := range entsSeen {
			if v > top {
				top = v
			}
		}
		fmt.Printf("单个实体最多出现=%d\n", top)
		fmt.Println("未采集到玩家 Moveable 下发（可能不在 AOI 或未移动）")
		return
	}
	fmt.Printf("携带 Moveable 的帧数 = %d（%.1f 帧/秒）\n", total, float64(total)/duration.Seconds())
	fmt.Printf("相邻帧位置变化 = %d, 不变 = %d（不变占比 %.1f%%）\n",
		changed, same, 100*float64(same)/float64(same+changed))
	fmt.Printf("不同位置取值数 = %d\n", len(seenValues))
	fmt.Printf("每秒变化次数 = %v\n", perSecond)
}
