// pomelo-stress 是多用户压力测试客户端：
// 多个客户端并发登录，每批发送多条不同方向的移动（复杂操作），
// 统计"操作发出 → 自己实体位置更新到达"的端到端延迟，
// 并验证每个客户端都能看到所有实体的变更。
//
// 用法：go run ./tools/pomelo-stress -clients 5 -duration 10s
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
)

// 移动模式：只用正增量，保证位置单调递增，便于延迟匹配。
var deltas = [][2]int{{1, 0}, {0, 1}, {1, 1}, {2, 0}, {0, 2}}

type pendingMove struct {
	finalX, finalY int
	sent           time.Time
}

type clientStats struct {
	id        int
	entity    uint64
	movesSent int
	updates   int // 自己实体的推送次数
	seen      map[uint64]int
	latencies []time.Duration
	unmatched int
}

type stressClient struct {
	id      int
	entity  uint64
	ownX    int
	ownY    int
	pending []pendingMove
	pushCh  chan *pomelo.Message
	conn    *websocket.Conn
	stats   clientStats
}

func main() {
	addr := flag.String("addr", "ws://localhost:8081/ws", "网关地址")
	clients := flag.Int("clients", 5, "并发客户端数")
	duration := flag.Duration("duration", 10*time.Second, "运行时长")
	interval := flag.Duration("interval", 100*time.Millisecond, "每批移动的间隔")
	moves := flag.Int("moves", 3, "每批移动条数（复杂操作：一批多条）")
	flag.Parse()

	var wg sync.WaitGroup
	stats := make([]*clientStats, *clients)
	for i := 0; i < *clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stats[i] = runClient(*addr, i+1, *interval, *moves, *duration)
		}(i)
	}
	wg.Wait()
	report(stats, *clients)
}

func runClient(addr string, id int, interval time.Duration, moves int, duration time.Duration) *clientStats {
	conn, _, err := websocket.DefaultDialer.Dial(addr, nil)
	if err != nil {
		log.Fatalf("client %d dial: %v", id, err)
	}
	defer conn.Close()

	// 握手 → ack → 登录
	writePacket(conn, pomelo.PacketHandshake, []byte(`{"version":"0.0.1"}`))
	if _, err := readPacket(conn); err != nil {
		log.Fatalf("client %d handshake: %v", id, err)
	}
	writePacket(conn, pomelo.PacketHandshakeAck, nil)
	loginReq, _ := pb.Marshal(&proto.LoginRequest{Token: fmt.Sprintf("u%d", id)})
	writeMessage(conn, pomelo.MsgRequest, 1, proto.RouteLogin, loginReq)
	resp, err := readMessage(conn)
	if err != nil {
		log.Fatalf("client %d login read: %v", id, err)
	}
	var lr proto.LoginResponse
	if err := pb.Unmarshal(resp.Data, &lr); err != nil || !lr.Success {
		log.Fatalf("client %d login failed", id)
	}

	c := &stressClient{
		id:     id,
		entity: lr.EntityId,
		pushCh: make(chan *pomelo.Message, 512),
		conn:   conn,
		stats:  clientStats{id: id, entity: lr.EntityId, seen: make(map[uint64]int)},
	}
	go c.readLoop()

	tick := time.NewTicker(interval)
	defer tick.Stop()
	deadline := time.After(duration)
	burst := 0

	for {
		select {
		case <-tick.C:
			// 一批多条移动：只记录批次的最终期望位置（服务器一次性应用缓冲命令）
			bdx, bdy := 0, 0
			for k := 0; k < moves; k++ {
				d := deltas[(burst+k)%len(deltas)]
				bdx += d[0]
				bdy += d[1]
			}
			data, _ := pb.Marshal(&proto.PlayerMove{Dx: int32(bdx), Dy: int32(bdy)})
			writeMessage(conn, pomelo.MsgNotify, 0, proto.RouteMove, data)
			c.ownX += bdx
			c.ownY += bdy
			c.pending = append(c.pending, pendingMove{finalX: c.ownX, finalY: c.ownY, sent: time.Now()})
			burst += moves
			c.stats.movesSent += moves

		case m := <-c.pushCh:
			c.handlePush(m)

		case <-deadline:
			c.stats.unmatched = len(c.pending)
			return &c.stats
		}
	}
}

func (c *stressClient) readLoop() {
	for {
		pkt, err := readPacket(c.conn)
		if err != nil {
			return
		}
		if pkt.Type != pomelo.PacketData {
			continue
		}
		m, err := pomelo.DecodeMessage(pkt.Data)
		if err != nil || m.Type != pomelo.MsgPush {
			continue
		}
		c.pushCh <- m
	}
}

func (c *stressClient) handlePush(m *pomelo.Message) {
	if m.Route != proto.RouteMove {
		return
	}
	var push proto.MovePush
	if err := pb.Unmarshal(m.Data, &push); err != nil {
		return
	}
	c.stats.seen[push.EntityId]++
	if push.EntityId != c.entity {
		return
	}
	c.stats.updates++

	// 位置单调递增：本次推送确认了所有 final <= push 的 pending
	// （服务器一个 tick 可能应用了多批，必须一次弹掉全部已确认的）
	now := time.Now()
	keep := c.pending[:0]
	for _, p := range c.pending {
		if int(push.X) >= p.finalX && int(push.Y) >= p.finalY {
			c.stats.latencies = append(c.stats.latencies, now.Sub(p.sent))
		} else {
			keep = append(keep, p)
		}
	}
	c.pending = keep
	// 清理超时 pending（3s 未匹配视为异常，防堆积）
	cut := 0
	for _, p := range c.pending {
		if now.Sub(p.sent) < 3*time.Second {
			c.pending[cut] = p
			cut++
		}
	}
	c.pending = c.pending[:cut]
}

func report(stats []*clientStats, clients int) {
	fmt.Printf("\n===== 多用户压力测试结果 =====\n")
	fmt.Printf("客户端数: %d\n", clients)

	// 互见验证：所有客户端看到的实体集合应一致（世界可能有历史实体，
	// 不硬性等于客户端数；以"最全的客户端"为准，缺了的算失败）
	maxSeen := 0
	for _, s := range stats {
		if len(s.seen) > maxSeen {
			maxSeen = len(s.seen)
		}
	}
	allSeen := true
	for _, s := range stats {
		if len(s.seen) < maxSeen {
			allSeen = false
			fmt.Printf("  [FAIL] client %d (entity %d): 只看到 %d/%d 个实体\n", s.id, s.entity, len(s.seen), maxSeen)
		}
	}
	if allSeen {
		fmt.Printf("互见验证: ✅ 所有 %d 个客户端都看到了全部 %d 个实体的变更\n", clients, maxSeen)
	}

	fmt.Printf("%-7s %-8s %-9s %-9s %-11s %-9s %-9s %-9s %-10s\n",
		"client", "entity", "sent", "updates", "min(ms)", "avg(ms)", "max(ms)", "p95(ms)", "unmatched")
	var allLat []time.Duration
	for _, s := range stats {
		lat := s.latencies
		min, avg, max, p95 := 0.0, 0.0, 0.0, 0.0
		if len(lat) > 0 {
			sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
			min = float64(lat[0].Microseconds()) / 1000
			max = float64(lat[len(lat)-1].Microseconds()) / 1000
			var sum time.Duration
			for _, l := range lat {
				sum += l
			}
			avg = float64((sum / time.Duration(len(lat))).Microseconds()) / 1000
			p95 = float64(lat[int(float64(len(lat))*0.95)-1].Microseconds()) / 1000
			allLat = append(allLat, lat...)
		}
		fmt.Printf("%-7d %-8d %-9d %-9d %-11.1f %-9.1f %-9.1f %-9.1f %-10d\n",
			s.id, s.entity, s.movesSent, s.updates, min, avg, max, p95, s.unmatched)
	}
	if len(allLat) > 0 {
		sort.Slice(allLat, func(i, j int) bool { return allLat[i] < allLat[j] })
		var sum time.Duration
		for _, l := range allLat {
			sum += l
		}
		fmt.Printf("总样本 %d：整体 avg=%.1fms p95=%.1fms max=%.1fms\n",
			len(allLat),
			float64((sum/time.Duration(len(allLat))).Microseconds())/1000,
			float64(allLat[int(float64(len(allLat))*0.95)-1].Microseconds())/1000,
			float64(allLat[len(allLat)-1].Microseconds())/1000)
	}
}

// ---- 协议辅助 ----

func writePacket(conn *websocket.Conn, t byte, body []byte) {
	wire, err := pomelo.EncodePacket(t, body)
	if err != nil {
		log.Fatalf("encode packet: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, wire); err != nil {
		log.Fatalf("write packet: %v", err)
	}
}

func writeMessage(conn *websocket.Conn, typ byte, mid uint64, route string, data []byte) {
	wire, err := pomelo.EncodeMessage(&pomelo.Message{Type: typ, ID: mid, Route: route, Data: data})
	if err != nil {
		log.Fatalf("encode message: %v", err)
	}
	writePacket(conn, pomelo.PacketData, wire)
}

func readPacket(conn *websocket.Conn) (*pomelo.Packet, error) {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	packets, err := pomelo.DecodePackets(raw)
	if err != nil {
		return nil, err
	}
	if len(packets) != 1 {
		return nil, fmt.Errorf("expected 1 packet, got %d", len(packets))
	}
	return packets[0], nil
}

func readMessage(conn *websocket.Conn) (*pomelo.Message, error) {
	pkt, err := readPacket(conn)
	if err != nil {
		return nil, err
	}
	if pkt.Type != pomelo.PacketData {
		return nil, fmt.Errorf("expected data packet, got type %d", pkt.Type)
	}
	return pomelo.DecodeMessage(pkt.Data)
}
