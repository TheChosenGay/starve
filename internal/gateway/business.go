package gateway

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/TheChosenGay/combet"
	feedsauth "github.com/TheChosenGay/feeds/pkg/auth"
	pb "google.golang.org/protobuf/proto"

	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/world"
	"starve/internal/gateway/pomelo"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

// Gateway 是 combet 的 Business 实现（同时实现 HandshakeHandler）：
// 处理握手协商、登录、游戏路由，把客户端消息翻译成世界 actor 的命令。
//
// 并发：combet 的读循环保证同一连接的消息串行进入 OnMessage；
// 跨连接并发由 Sessions/Router 的锁保护。
type Gateway struct {
	core      *comet.Core // 用于回复/推送单个连接
	engine    *actor.Engine
	worldPID  *actor.PID
	router    *Router
	sessions  *Sessions
	logger    *slog.Logger
	sweepStop chan struct{}
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
	g.router.Register(proto.RouteLogin, RouteEntry{MsgType: (*proto.LoginRequest)(nil), Target: TargetAgent})
	g.router.Register(proto.RouteMove, RouteEntry{MsgType: (*proto.PlayerMove)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteGather, RouteEntry{MsgType: (*proto.PlayerGather)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteAttack, RouteEntry{MsgType: (*proto.PlayerAttack)(nil), Target: TargetWorld})
	g.router.Register(proto.RoutePickup, RouteEntry{MsgType: (*proto.PlayerPickup)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteUse, RouteEntry{MsgType: (*proto.PlayerUse)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteEquip, RouteEntry{MsgType: (*proto.PlayerEquip)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteChop, RouteEntry{MsgType: (*proto.PlayerChop)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteMine, RouteEntry{MsgType: (*proto.PlayerMine)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteAutomate, RouteEntry{MsgType: (*proto.PlayerAutomate)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteDrop, RouteEntry{MsgType: (*proto.PlayerDrop)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteCraft, RouteEntry{MsgType: (*proto.PlayerCraft)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteCancelCraft, RouteEntry{MsgType: (*proto.PlayerCancelCraft)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteSplit, RouteEntry{MsgType: (*proto.PlayerSplit)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteBuild, RouteEntry{MsgType: (*proto.Build)(nil), Target: TargetWorld})
	g.router.Register(proto.RoutePlace, RouteEntry{MsgType: (*proto.Place)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteBuildCheck, RouteEntry{MsgType: (*proto.BuildCheck)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteDemolish, RouteEntry{MsgType: (*proto.Demolish)(nil), Target: TargetWorld})
	g.router.Register(proto.RouteSave, RouteEntry{Target: TargetAgent})
	return g
}

// AttachCore 注入 combet Core（用于回复/推送单个连接）。
func (g *Gateway) AttachCore(core *comet.Core) { g.core = core }

// StartSweeper 启动断线检测：combet 没有 OnClose 回调，靠轮询 ConnManager
// 发现连接已关闭 → 移除会话并通知世界（离线保留）。
func (g *Gateway) StartSweeper(interval time.Duration) {
	if g.sweepStop != nil {
		return
	}
	g.sweepStop = make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				g.sweepOnce()
			case <-g.sweepStop:
				return
			}
		}
	}()
}

// StopSweeper 停止断线检测（关服前调用）。
func (g *Gateway) StopSweeper() {
	if g.sweepStop != nil {
		close(g.sweepStop)
		g.sweepStop = nil
	}
}

func (g *Gateway) sweepOnce() {
	if g.core == nil {
		return
	}
	for _, sess := range g.sessions.All() {
		if _, ok := g.core.ConnManager().Get(sess.ConnID); ok {
			continue
		}
		if g.sessions.RemoveByConn(sess.ConnID) == nil {
			continue
		}
		g.logger.Info("session disconnected", "uid", sess.UID)
		g.engine.Send(g.worldPID, world.PlayerDisconnect{UID: sess.UID})
	}
}

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
		switch msg.Route {
		case proto.RouteLogin:
			g.handleLogin(connID, msg)
		case proto.RouteSave:
			g.handleSave(connID, msg)
		}
	case TargetWorld:
		switch msg.Route {
		case proto.RouteMove:
			g.handleMove(connID, msg)
		case proto.RouteGather:
			g.handleGather(connID, msg)
		case proto.RouteAttack:
			g.handleAttack(connID, msg)
		case proto.RoutePickup:
			g.handlePickup(connID, msg)
		case proto.RouteUse:
			g.handleUse(connID, msg)
		case proto.RouteEquip:
			g.handleEquip(connID, msg)
		case proto.RouteChop:
			g.handleChop(connID, msg)
		case proto.RouteMine:
			g.handleMine(connID, msg)
		case proto.RouteAutomate:
			g.handleAutomate(connID, msg)
		case proto.RouteDrop:
			g.handleDrop(connID, msg)
		case proto.RouteCraft:
			g.handleCraft(connID, msg)
		case proto.RouteCancelCraft:
			g.handleCancelCraft(connID, msg)
		case proto.RouteSplit:
			g.handleSplit(connID, msg)
		case proto.RouteBuild:
			g.handleBuild(connID, msg)
		case proto.RoutePlace:
			g.handlePlace(connID, msg)
		case proto.RouteBuildCheck:
			g.handleBuildCheck(connID, msg)
		case proto.RouteDemolish:
			g.handleDemolish(connID, msg)
		}
	}
	return nil
}

// handleSave 客户端点存档：触发世界 actor 保存，回复结果。
func (g *Gateway) handleSave(connID string, msg *pomelo.Message) {
	if _, ok := g.sessions.GetByConn(connID); !ok {
		return // 未登录不响应
	}
	resp := g.engine.Request(g.worldPID, world.SaveRequest{}, 5*time.Second)
	_, err := resp.Wait()
	g.reply(connID, msg.ID, &proto.SaveResponse{Success: err == nil})
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
	// 真实用户系统：token 由 feeds 的 user 服务签发（JWT，HS256，同 JWT_SECRET），
	// 复用 feeds/pkg/auth.ValidateToken 校验并取 user_id 作为世界内玩家 UID。
	uid, err := feedsauth.ValidateToken(req.Token)
	if err != nil || uid == "" {
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
	// 全量快照（登录后一次性下发，客户端重建实体表）
	if snap := g.requestSnapshot(); snap != nil {
		g.pushProto(connID, proto.RouteSnapshot, snap)
	}
	// 世界静态配置（模板/配方/工作站，客户端渲染用）
	if cfg := g.requestConfig(); cfg != nil {
		g.pushProto(connID, proto.RouteConfig, cfg)
	}
}

func (g *Gateway) requestSnapshot() *game.Snapshot {
	resp := g.engine.Request(g.worldPID, world.QuerySnapshot{}, 2*time.Second)
	v, err := resp.Wait()
	if err != nil {
		return nil
	}
	snap, ok := v.(*game.Snapshot)
	if !ok {
		return nil
	}
	return snap
}

func (g *Gateway) requestConfig() *game.GameConfig {
	resp := g.engine.Request(g.worldPID, world.QueryConfig{}, 2*time.Second)
	v, err := resp.Wait()
	if err != nil {
		return nil
	}
	cfg, ok := v.(*game.GameConfig)
	if !ok {
		return nil
	}
	return cfg
}

// pushProto 组 pomelo push 写回连接。
func (g *Gateway) pushProto(connID, route string, m pb.Message) {
	data, err := pb.Marshal(m)
	if err != nil {
		return
	}
	wire, err := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgPush, Route: route, Data: data})
	if err != nil {
		return
	}
	g.core.Send(connID, &comet.Msg{Type: comet.MsgData, Payload: wire})
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

// handleGather 采集指令（notify）：目标实体由客户端从快照（带 Workable{PICK} 组件）选取。
func (g *Gateway) handleGather(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("gather from unauthenticated conn", "conn", connID)
		return
	}
	var gr proto.PlayerGather
	if err := pb.Unmarshal(msg.Data, &gr); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandGather,
		Data: world.GatherData{Player: sess.EntityID, Target: ecs.Entity(gr.TargetEntity)},
	})
}

// handleAttack 攻击指令（notify）：攻击者 = 会话实体，目标由客户端从快照选取。
func (g *Gateway) handleAttack(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("attack from unauthenticated conn", "conn", connID)
		return
	}
	var at proto.PlayerAttack
	if err := pb.Unmarshal(msg.Data, &at); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandAttack,
		Data: world.AttackData{Attacker: sess.EntityID, Target: ecs.Entity(at.TargetEntity)},
	})
}

// handlePickup 拾取指令（notify）：目标 = 带 Loot 的掉落物实体。
func (g *Gateway) handlePickup(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("pickup from unauthenticated conn", "conn", connID)
		return
	}
	var pk proto.PlayerPickup
	if err := pb.Unmarshal(msg.Data, &pk); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandPickup,
		Data: world.PickupData{Player: sess.EntityID, Target: ecs.Entity(pk.LootEntity)},
	})
}

// handleUse 使用指令（notify）：kind = ItemKind 枚举值。
func (g *Gateway) handleUse(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("use from unauthenticated conn", "conn", connID)
		return
	}
	var u proto.PlayerUse
	if err := pb.Unmarshal(msg.Data, &u); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandUse,
		Data: world.UseData{Player: sess.EntityID, Kind: components.ItemKind(u.Kind)},
	})
}

// handleEquip 装备/卸下工具（kind=0 卸下）。
func (g *Gateway) handleEquip(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("equip from unauthenticated conn", "conn", connID)
		return
	}
	var e proto.PlayerEquip
	if err := pb.Unmarshal(msg.Data, &e); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandEquip,
		Data: world.EquipData{Player: sess.EntityID, Kind: components.ItemKind(e.Kind)},
	})
}

func (g *Gateway) handleChop(connID string, msg *pomelo.Message) {
	g.handleWork(connID, msg, world.CommandChop)
}

func (g *Gateway) handleMine(connID string, msg *pomelo.Message) {
	g.handleWork(connID, msg, world.CommandMine)
}

// handleAutomate 空格自动行为指令（notify）：服务端在玩家 AOI 范围内
// 按距离就近匹配可执行行为并执行一次（客户端不传目标）。
func (g *Gateway) handleAutomate(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("automate from unauthenticated conn", "conn", connID)
		return
	}
	var au proto.PlayerAutomate
	if err := pb.Unmarshal(msg.Data, &au); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandAutomate,
		Data: world.AutomateData{Player: sess.EntityID},
	})
}

// handleDrop 丢弃指令：kind/count。
func (g *Gateway) handleDrop(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("drop from unauthenticated conn", "conn", connID)
		return
	}
	var d proto.PlayerDrop
	if err := pb.Unmarshal(msg.Data, &d); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandDrop,
		Data: world.DropData{Player: sess.EntityID, Kind: components.ItemKind(d.Kind), Count: int(d.Count)},
	})
}

// handleCraft 制作请求（request/response）：世界校验并开始制作，返回时长/错误。
func (g *Gateway) handleCraft(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		return
	}
	var req proto.PlayerCraft
	if err := pb.Unmarshal(msg.Data, &req); err != nil {
		return
	}
	resp := g.engine.Request(g.worldPID, world.CraftRequest{UID: sess.UID, RecipeID: req.RecipeId}, 5*time.Second)
	v, err := resp.Wait()
	cr := proto.CraftResponse{Message: "world_unavailable"}
	if err == nil {
		if r, ok := v.(world.CraftResult); ok {
			cr = proto.CraftResponse{Started: r.Started, Message: r.Message, Ticks: int32(r.Ticks)}
		}
	}
	g.reply(connID, msg.ID, &cr)
}

// handleCancelCraft 主动取消制作（notify）。
func (g *Gateway) handleCancelCraft(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandCancelCraft,
		Data: world.CancelCraftData{Player: sess.EntityID},
	})
}

// handleSplit 拆分背包物品（notify）：from_slot + count。
func (g *Gateway) handleSplit(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		return
	}
	var s proto.PlayerSplit
	if err := pb.Unmarshal(msg.Data, &s); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandSplit,
		Data: world.SplitData{Player: sess.EntityID, FromSlot: int(s.FromSlot), Count: int(s.Count)},
	})
}

// handleBuild 建造请求（request）：kind → 创建未放置的建筑实体，返回实体 id。
func (g *Gateway) handleBuild(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("build from unauthenticated conn", "conn", connID)
		return
	}
	var b proto.Build
	if err := pb.Unmarshal(msg.Data, &b); err != nil {
		return
	}
	resp := g.engine.Request(g.worldPID, world.BuildRequest{UID: sess.UID, Kind: components.BuildingKind(b.Kind)}, 5*time.Second)
	v, err := resp.Wait()
	br := proto.BuildResponse{Message: "world_unavailable"}
	if err == nil {
		if r, ok := v.(world.BuildResult); ok {
			br = proto.BuildResponse{Ok: r.Started, Entity: uint64(r.Entity), Message: r.Message}
		}
	}
	g.reply(connID, msg.ID, &br)
}

// handlePlace 放置指令（notify）：把已创建建筑放到坐标。
func (g *Gateway) handlePlace(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("place from unauthenticated conn", "conn", connID)
		return
	}
	var p proto.Place
	if err := pb.Unmarshal(msg.Data, &p); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandPlace,
		Data: world.PlaceData{Actor: sess.EntityID, Entity: ecs.Entity(p.Entity), X: int(p.X), Y: int(p.Y)},
	})
}

// handleBuildCheck 建造可放置查询（request）：返回 ok（客户端幽灵预览）。
func (g *Gateway) handleBuildCheck(connID string, msg *pomelo.Message) {
	if _, ok := g.sessions.GetByConn(connID); !ok {
		return
	}
	var q proto.BuildCheck
	if err := pb.Unmarshal(msg.Data, &q); err != nil {
		return
	}
	resp := g.engine.Request(g.worldPID, world.QueryCanPlace{Entity: ecs.Entity(q.Entity), X: int(q.X), Y: int(q.Y)}, 5*time.Second)
	v, err := resp.Wait()
	placeable := false
	if err == nil {
		if b, ok := v.(bool); ok {
			placeable = b
		}
	}
	g.reply(connID, msg.ID, &proto.BuildCheckResponse{Ok: placeable})
}

// handleDemolish 拆除指令（notify）：目标建筑实体。
func (g *Gateway) handleDemolish(connID string, msg *pomelo.Message) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("demolish from unauthenticated conn", "conn", connID)
		return
	}
	var d proto.Demolish
	if err := pb.Unmarshal(msg.Data, &d); err != nil {
		return
	}
	g.engine.Send(g.worldPID, world.Command{
		UID:  sess.UID,
		Kind: world.CommandDemolish,
		Data: world.DemolishData{Actor: sess.EntityID, Target: ecs.Entity(d.TargetEntity)},
	})
}

// handleWork 砍伐/挖掘共用：解析目标实体并投递。
func (g *Gateway) handleWork(connID string, msg *pomelo.Message, kind world.CommandKind) {
	sess, ok := g.sessions.GetByConn(connID)
	if !ok {
		g.logger.Warn("work from unauthenticated conn", "conn", connID)
		return
	}
	switch kind {
	case world.CommandChop:
		var m proto.PlayerChop
		if err := pb.Unmarshal(msg.Data, &m); err != nil {
			return
		}
		g.engine.Send(g.worldPID, world.Command{UID: sess.UID, Kind: world.CommandChop,
			Data: world.ChopData{Player: sess.EntityID, Target: ecs.Entity(m.TargetEntity)}})
	case world.CommandMine:
		var m proto.PlayerMine
		if err := pb.Unmarshal(msg.Data, &m); err != nil {
			return
		}
		g.engine.Send(g.worldPID, world.Command{UID: sess.UID, Kind: world.CommandMine,
			Data: world.MineData{Player: sess.EntityID, Target: ecs.Entity(m.TargetEntity)}})
	}
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

// HandlePush 处理世界 outbox 的推送效果（由 WorldActor.SetPushSink 注入调用）。
// To 为空 → 广播给所有在线会话；否则推给指定连接。
func (g *Gateway) HandlePush(pe world.PushEffect) {
	if pe.Payload == nil {
		return
	}
	m, ok := pe.Payload.(pb.Message)
	if !ok {
		g.logger.Warn("push payload not proto.Message", "route", pe.Route)
		return
	}
	data, err := pb.Marshal(m)
	if err != nil {
		return
	}
	wire, err := pomelo.EncodeMessage(&pomelo.Message{Type: pomelo.MsgPush, Route: pe.Route, Data: data})
	if err != nil {
		return
	}
	if pe.To == "" {
		for _, sess := range g.sessions.All() {
			g.core.Send(sess.ConnID, &comet.Msg{Type: comet.MsgData, Payload: wire})
		}
		return
	}
	g.core.Send(pe.To, &comet.Msg{Type: comet.MsgData, Payload: wire})
}
