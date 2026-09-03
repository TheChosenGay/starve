// 大厅服务入口：HTTP（登录/匹配）+ 自己的 actor engine（注册进 actor 集群）。
// 匹配完成 → 向世界服务器节点的 NodeManager 发 CreateWorld（跨节点集群消息）。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/TheChosenGay/actor"
	"github.com/TheChosenGay/actor/cluster"

	"starve/internal/game/config"
	"starve/internal/game/world"
)

type createMatch struct{}
type createMatchResp struct {
	RoomName    string `json:"room_name"`
	AccessToken string `json:"access_token"`
	WorldAddr   string `json:"world_addr"`
}

type lobbyActor struct {
	engine    *actor.Engine
	worldAddr string
	counter   atomic.Uint64
}

func (l *lobbyActor) Receive(ctx actor.IActorContext) {
	if _, ok := ctx.Message().(createMatch); !ok {
		return
	}
	n := l.counter.Add(1)
	roomName := fmt.Sprintf("room-%d", n)
	token := randomToken()
	r := l.engine.Request(&actor.PID{Address: l.worldAddr, ID: "worldnode/manager"},
		world.CreateWorld{RoomName: roomName, AccessToken: token}, 5*time.Second)
	v, err := r.Wait()
	if err != nil {
		ctx.Respond(fmt.Errorf("create world: %w", err))
		return
	}
	_ = v.(world.CreateWorldResp)
	ctx.Respond(createMatchResp{RoomName: roomName, AccessToken: token, WorldAddr: l.worldAddr})
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	httpAddr := config.EnvOr("STARVE_LOBBY_ADDR", ":8080")
	clusterAddr := config.EnvOr("STARVE_LOBBY_CLUSTER_ADDR", ":8082")
	clusterListen := config.EnvOr("STARVE_LOBBY_CLUSTER_LISTEN", clusterAddr)
	natsURL := config.EnvOr("STARVE_NATS_URL", "nats://127.0.0.1:4222")
	worldAddr := config.EnvOr("STARVE_WORLD_NODE_ADDR", "127.0.0.1:8082")

	engine := actor.NewEngine(actor.Config{
		Cluster: actor.ClusterConfig{
			Enable:     true,
			NodeAddr:   clusterAddr,
			ListenAddr: clusterListen,
			NodeID:     config.EnvOr("STARVE_NODE_ID", "lobby-1"),
			Transport:  actor.NewTCPTransport(),
			Client: cluster.NewNATSClient(actor.ClusterConfig{
				NodeAddr:          clusterAddr,
				ListenAddr:        clusterListen,
				NodeID:            config.EnvOr("STARVE_NODE_ID", "lobby-1"),
				RegisterTimeout:   5 * time.Second,
				HeartbeatInterval: 3 * time.Second,
				LeaseTTL:          9 * time.Second,
			}, natsURL, "lobby"),
		},
	})
	defer engine.Shutdown()
	go func() { _ = engine.StartCluster(ctx) }()

	lobby := &lobbyActor{engine: engine, worldAddr: worldAddr}
	lobbyPID := engine.Spawn(func() actor.IActor { return lobby }, "lobby", "manager")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /rooms", func(w http.ResponseWriter, _ *http.Request) {
		r := engine.Request(lobbyPID, createMatch{}, 10*time.Second)
		v, err := r.Wait()
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(v)
	})
	srv := &http.Server{Addr: httpAddr, Handler: mux}
	slog.Info("lobby ready", "addr", httpAddr, "world_addr", worldAddr)
	go func() { _ = srv.ListenAndServe() }()
	<-ctx.Done()
	_ = srv.Shutdown(ctx)
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
