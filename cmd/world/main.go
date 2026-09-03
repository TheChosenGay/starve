// 世界服务器节点入口：一个节点 = 一个 actor engine + NodeManager（多个 WorldActor 子节点）
// + 共享 combet 网关（按 accessToken 路由到房间）。节点注册进 actor 集群（NATS），供大厅发现。
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TheChosenGay/actor"
	"github.com/TheChosenGay/actor/cluster"
	"github.com/TheChosenGay/combet"
	"github.com/TheChosenGay/combet/ws"

	"starve/internal/game/config"
	"starve/internal/game/world"
	"starve/internal/gateway"
	"starve/internal/gateway/pomelo"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	wsAddr := config.EnvOr("STARVE_WORLD_WS_ADDR", ":8081")
	clusterAddr := config.EnvOr("STARVE_WORLD_CLUSTER_ADDR", ":8082")
	clusterListen := config.EnvOr("STARVE_WORLD_CLUSTER_LISTEN", clusterAddr)
	natsURL := config.EnvOr("STARVE_NATS_URL", "nats://127.0.0.1:4222")
	registryAddr := config.EnvOr("STARVE_REGISTRY_ADDR", "registry:8081")
	nodeID := config.EnvOr("STARVE_NODE_ID", "world-1")
	saveRoot := config.EnvOr("STARVE_SAVE_ROOT", "data/worlds")

	cm := config.NewConfigManagerFromEnv()
	cfg := cm.WorldConfig()
	gc, err := cm.Load()
	if err != nil {
		slog.Error("load configs", "err", err)
		os.Exit(1)
	}

	engine := actor.NewEngine(actor.Config{
		Cluster: actor.ClusterConfig{
			Enable:       true,
			RegistryAddr: registryAddr,
			NodeAddr:     clusterAddr,
			ListenAddr:   clusterListen,
			NodeID:       nodeID,
			Transport:    actor.NewTCPTransport(),
			Client: cluster.NewNATSClient(actor.ClusterConfig{
				RegistryAddr:      registryAddr,
				NodeAddr:          clusterAddr,
				ListenAddr:        clusterListen,
				NodeID:            nodeID,
				Transport:         actor.NewTCPTransport(),
				RegisterTimeout:   5 * time.Second,
				HeartbeatInterval: 3 * time.Second,
				LeaseTTL:          9 * time.Second,
			}, natsURL, "world"),
		},
	})
	defer engine.Shutdown()
	go func() { _ = engine.StartCluster(ctx) }()

	// NodeManager：创建/查询/销毁世界，并按房间名持久化。
	nodePID := engine.Spawn(world.NewWorldNode(cfg, gc, saveRoot), "worldnode", "manager")

	// 共享 combet 网关：握手按 accessToken 查 NodeManager → 路由到对应世界。
	gw := gateway.NewGateway(engine, nil)
	gw.SetRoomResolver(func(token string) (*actor.PID, string, bool) {
		r := engine.Request(nodePID, world.QueryToken{AccessToken: token}, 2*time.Second)
		v, err := r.Wait()
		if err != nil {
			return nil, "", false
		}
		qt, ok := v.(world.QueryTokenResp)
		return qt.PID, qt.RoomName, ok && qt.OK
	})
	core := comet.NewCore(comet.ServerConfig{Business: gw, Scheme: pomelo.NewScheme()})
	gw.AttachCore(core)

	slog.Info("world node ready", "ws", wsAddr, "cluster", clusterAddr, "node", nodeID, "save_root", saveRoot)
	srv := ws.NewServerWithCore(wsAddr, core)
	if err := srv.Start(ctx); err != nil {
		slog.Error("ws serve", "err", err)
		os.Exit(1)
	}
}
