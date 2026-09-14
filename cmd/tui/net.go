package main

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/devjwt"
	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

// client 是"网关 WS 连接 + 协议状态"的薄封装：握手、登录、收推送、发 notify。
// 与 tools/pomelo-client 用同一套编解码，保证 TUI 不会因为协议细节漂移而失效。
type client struct {
	conn   *websocket.Conn
	uid    string
	entity uint64
	input  uint64 // 登录返回的输入世代（移动命令要带，否则服务端按旧世代丢弃）

	push chan *pomelo.Message

	mu    sync.Mutex
	world *world

	readyOnce sync.Once
	ready     chan struct{}
	fail      chan error
}

// dial 建立连接并完成 握手 → ack → 登录，返回可用客户端。
func dial(addr, uid, token, access string) (*client, error) {
	conn, _, err := websocket.DefaultDialer.Dial(addr, nil)
	if err != nil {
		return nil, fmt.Errorf("连接 %s: %w", addr, err)
	}
	c := &client{
		conn:  conn,
		uid:   uid,
		push:  make(chan *pomelo.Message, 256),
		world: newWorld(),
		ready: make(chan struct{}),
		fail:  make(chan error, 1),
	}

	// 1. 握手 → 响应 → ack（access_token 可选：本地单世界不需要房间票据）
	hsPayload := map[string]string{"version": "0.0.1"}
	if access != "" {
		hsPayload["access_token"] = access
	}
	hsData, err := json.Marshal(hsPayload)
	if err != nil {
		return nil, err
	}
	if err := c.writePacket(pomelo.PacketHandshake, hsData); err != nil {
		return nil, err
	}
	hs, err := c.readPacket()
	if err != nil {
		return nil, fmt.Errorf("握手: %w", err)
	}
	_ = hs
	if err := c.writePacket(pomelo.PacketHandshakeAck, nil); err != nil {
		return nil, err
	}

	// 2. 登录（token 为空则按 uid 自签 dev token，与 tools/pomelo-client 一致）
	loginToken := token
	if loginToken == "" {
		loginToken = devjwt.Mint(uid)
	}
	req, err := pb.Marshal(&proto.LoginRequest{Token: loginToken})
	if err != nil {
		return nil, err
	}
	if err := c.writeMessage(pomelo.MsgRequest, 1, proto.RouteLogin, req); err != nil {
		return nil, err
	}
	resp, err := c.readMessage()
	if err != nil {
		return nil, fmt.Errorf("登录: %w", err)
	}
	var lr proto.LoginResponse
	if err := pb.Unmarshal(resp.Data, &lr); err != nil {
		return nil, fmt.Errorf("解析登录响应: %w", err)
	}
	if !lr.Success {
		return nil, fmt.Errorf("登录被拒: %s", lr.Message)
	}
	c.entity, c.input = uint64(lr.EntityId), lr.InputEpoch

	// 3. 后台收推送
	go c.readLoop()
	return c, nil
}

// readLoop 持续读包，把 push 交给主循环；被踢/断线时通知失败。
func (c *client) readLoop() {
	for {
		pkt, err := c.readPacket()
		if err != nil {
			c.failOnce(fmt.Errorf("连接断开: %w", err))
			return
		}
		switch pkt.Type {
		case pomelo.PacketKick:
			c.failOnce(fmt.Errorf("被服务器踢出: %s", pkt.Data))
			return
		case pomelo.PacketData:
			m, err := pomelo.DecodeMessage(pkt.Data)
			if err != nil {
				continue
			}
			if m.Type == pomelo.MsgPush {
				select {
				case c.push <- m:
				default: // 渲染跟不上时丢帧：TUI 不是权威状态，下一帧快照会补齐
				}
			}
		}
	}
}

func (c *client) failOnce(err error) {
	select {
	case c.fail <- err:
	default:
	}
}

// waitReady 等到世界配置到位（地形 + 模板），或者超时/断线。
func (c *client) waitReady(timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		select {
		case m := <-c.push:
			c.applyPush(m)
			if c.world.ready() {
				c.world.setOwn(c.entity)
				return nil
			}
		case err := <-c.fail:
			return err
		case <-deadline:
			return fmt.Errorf("等待快照/配置超时（%s）", timeout)
		}
	}
}

// applyPush 把推送应用到世界状态（主循环与 waitReady 共用同一条路径）。
func (c *client) applyPush(m *pomelo.Message) {
	switch m.Route {
	case proto.RouteSnapshot:
		var snap game.Snapshot
		if pb.Unmarshal(m.Data, &snap) == nil {
			c.world.applySnapshot(&snap)
			c.world.setOwn(c.entity)
		}
	case proto.RouteSnapshotDelta:
		var delta game.SnapshotDelta
		if pb.Unmarshal(m.Data, &delta) == nil {
			c.world.applyDelta(&delta)
		}
	case proto.RouteConfig:
		var cfg game.GameConfig
		if pb.Unmarshal(m.Data, &cfg) == nil {
			c.world.applyConfig(&cfg)
		}
	}
}

// ---- 发送 ----

// sendMove 发送持续方向意图（0,0 = 停）。服务端是"方向保持"，不是逐帧位移。
func (c *client) sendMove(dx, dy int) error {
	data, err := pb.Marshal(&proto.PlayerMove{Dx: int32(dx), Dy: int32(dy)})
	if err != nil {
		return err
	}
	return c.writeMessage(pomelo.MsgNotify, 0, proto.RouteMove, data)
}

// sendAutomate 空格自动行为：服务端按 AOI 就近匹配并走过去/执行。
func (c *client) sendAutomate() error {
	data, err := pb.Marshal(&proto.PlayerAutomate{})
	if err != nil {
		return err
	}
	return c.writeMessage(pomelo.MsgNotify, 0, proto.RouteAutomate, data)
}

// sendWork 对目标发砍伐或挖掘（按 Workable.Action 选路线）。
func (c *client) sendWork(target uint64, action game.WorkAction) error {
	var (
		data []byte
		err  error
		rt   string
	)
	switch action {
	case game.WorkAction_WORK_ACTION_CHOP:
		rt = proto.RouteChop
		data, err = pb.Marshal(&proto.PlayerChop{TargetEntity: target})
	case game.WorkAction_WORK_ACTION_MINE:
		rt = proto.RouteMine
		data, err = pb.Marshal(&proto.PlayerMine{TargetEntity: target})
	default:
		// 采集（灌木/花等）：走采集路线
		rt = proto.RouteGather
		data, err = pb.Marshal(&proto.PlayerGather{TargetEntity: target})
	}
	if err != nil {
		return err
	}
	return c.writeMessage(pomelo.MsgNotify, 0, rt, data)
}

// sendPickup 拾取掉落物实体。
func (c *client) sendPickup(loot uint64) error {
	data, err := pb.Marshal(&proto.PlayerPickup{LootEntity: loot})
	if err != nil {
		return err
	}
	return c.writeMessage(pomelo.MsgNotify, 0, proto.RoutePickup, data)
}

func (c *client) close() {
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// ---- 编解码（照搬 pomelo-client 的线格式）----

func (c *client) writePacket(t byte, body []byte) error {
	wire, err := pomelo.EncodePacket(t, body)
	if err != nil {
		return err
	}
	return c.conn.WriteMessage(websocket.BinaryMessage, wire)
}

func (c *client) readPacket() (*pomelo.Packet, error) {
	_, raw, err := c.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	packets, err := pomelo.DecodePackets(raw)
	if err != nil {
		return nil, err
	}
	if len(packets) != 1 {
		return nil, fmt.Errorf("期望 1 个包，收到 %d", len(packets))
	}
	return packets[0], nil
}

func (c *client) writeMessage(typ byte, mid uint64, route string, data []byte) error {
	wire, err := pomelo.EncodeMessage(&pomelo.Message{Type: typ, ID: mid, Route: route, Data: data})
	if err != nil {
		return err
	}
	return c.writePacket(pomelo.PacketData, wire)
}

func (c *client) readMessage() (*pomelo.Message, error) {
	pkt, err := c.readPacket()
	if err != nil {
		return nil, err
	}
	if pkt.Type != pomelo.PacketData {
		return nil, fmt.Errorf("期望数据包，收到类型 %d", pkt.Type)
	}
	return pomelo.DecodeMessage(pkt.Data)
}
