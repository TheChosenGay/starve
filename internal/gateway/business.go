package gateway

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/TheChosenGay/combet"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/world"
	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
)

// 路由常量（客户端/服务端契约，见 pkg/proto/message.proto）。
const (
	RouteLogin = "gate.login"
	RouteMove  = "world.player.move"
)

// Gateway 是 combet 的 Business 实现（同时实现 HandshakeHandler）：
// 处理握手协商、登录、游戏路由，把客户端消息翻译成世界 actor 的命令。
//
// 并发：combet 的读循环保证同一连接的消息串行进入 OnMessage；
// 跨连接并发由 Sessions/Router 的锁保护。
type Gateway struct {
	core     *comet.Core // 用于回复/推送单个连接
	engine   *actor.Engine
	worldPID *actor.PID
	router   *Router
	sessions *Sessions
	logger   *slog.Logger
}

// NewGateway 创建网关业务层。
// combet.Core 与 Business 循环依赖，按 feeds live 的模式：
// 先建 Gateway，再 NewCore(Business: gw)，最后 AttachCore(core)。
func NewGateway(engine *actor.Engine, worldPID *actor.PID) *Gateway {
	g := &Gateway{
		engine:   engine,
		worldPID: worldPID,
		router:   NewRouter(),
		sessions: NewSessions(),
		logger:   slog.With("component", "gateway"),
	}
	g.router.Register(RouteLogin, RouteEntry{MsgType: (*proto.LoginRequest)(nil), Target: TargetAgent})
	g.router.Register(RouteMove, RouteEntry{MsgType: (*proto.PlayerMove)(nil), Target: TargetWorld})
	return g
}

// AttachCore 注入 combet Core（用于回复/推送单个连接）。
func (g *Gateway) AttachCore(core *comet.Core) { g.core = core }

// Sessions 暴露会话表（推送/统计用）。
func (g *Gateway) Sessions() *Sessions { return g.sessions }

// OnHandshake 实现 comet.HandshakeHandler：pomelo 握手协商（版本/心跳）。
func (g *Gateway) OnHandshake(_ context.Context, _ comet.Conn, _ []byte) ([]byte, error) {
	// MVP：固定协商内容；heartbeat 单位毫秒，30s
	return []byte(`{"code":200,"sys":{"heartbeat":30000}}`), nil
}

// OnAuth 实现 comet.Business（旧模式"握手即鉴权"路径）。
// pomelo 走握手阶段（HandshakeHandler），此方法不会被调用；实现以满足接口。
func (g *Gateway) OnAuth(_ context.Context, _ []byte) (string, error) {
	return "", errors.New("gateway: auth via login only")
}

// OnMessage 实现 combet.Business：解析 pomelo 消息并按 route 分发。
func (g *Gateway) OnMessage(_ context.Context, connID, _ string, payload []byte) error {
	msg, err := pomelo.DecodeMessage(payload)
	if err != nil {
		g.logger.Warn("decode message", "conn", connID, "err", err)
		return nil
	}
	entry, ok := g.router.Resolve(msg.Route)
	if !ok {
		g.logger.Warn("unknown route", "route", msg.Route, "conn", connID)
		return nil
	}
	switch entry.Target {
	case TargetAgent:
		g.handleLogin(connID, msg)
	case TargetWorld:
		g.handleMove(connID, msg)
	}
	return nil
}

func (g *Gateway) handleLogin(connID string, msg *pomelo.Message) {
	fail := func(code string) {
		g.reply(connID, msg.ID, &proto.LoginResponse{Success: false, Message: code})
	}
	var req proto.LoginRequest
	if err := pb.Unmarshal(msg.Data, &req); err != nil {
		fail("bad_request")
		return
	}
	uid, ok := authenticateStub(req.Token)
	if !ok {
		fail("bad_token")
		return
	}
	// 同 UID 重复登录：踢旧连接
	if old := g.sessions.Bind(uid, connID, 0); old != nil && old.ConnID != connID {
		g.core.Send(old.ConnID, &comet.Msg{Type: comet.MsgKick, Payload: []byte("kicked by new login")})
	}
	// 世界创建玩家实体（请求-应答，网关在连接读循环里等待，非 tick 内）
	resp := g.engine.Request(g.worldPID, world.CreatePlayer{UID: uid}, 2*time.Second)
	v, err := resp.Wait()
	if err != nil {
		fail("world_unavailable")
		return
	}
	entity, ok := v.(ecs.Entity)
	if !ok {
		fail("world_error")
		return
	}
	g.sessions.Bind(uid, connID, entity)
	g.reply(connID, msg.ID, &proto.LoginResponse{Success: true, UserId: uid, EntityId: uint64(entity)})
}

func (g *Gateway) handleMove(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("move from unauthenticated conn", "conn", connID)
		return
	}
	var mv proto.PlayerMove
	if err := pb.Unmarshal(msg.Data, &mv); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandMove,
		Data: world.MoveData{Entity: sess.EntityID, DX: int(mv.Dx), DY: int(mv.Dy)},
	})
}

// reply 组 pomelo response 写回连接（mid 关联：响应携带请求 mid）。
func (g *Gateway) reply(connID string, mid uint64, m pb.Message) {
	data, err := pb.Marshal(m)
	if err != nil {
		return
	}
	wire, err := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgResponse, ID: mid, Data: data})
	if err != nil {
		return
	}
	g.core.Send(connID, &comet.Msg{Type: comet.MsgData, Payload: wire})
}

// authenticateStub 是 MVP 占位鉴权：token = "u<uid>"。
// 接入真实用户服务（复用 feeds 的注册方案：独立库 + bcrypt + JWT）后替换。
func authenticateStub(token string) (string, bool) {
	if len(token) > 1 && token[0] == 'u' {
		return token[1:], true
	}
	return "", false
}
