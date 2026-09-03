// 集群服务中心节点：承载 actor 集群的 NATS 版 ServiceCenter（注册/心跳/广播）。
// 它是集群的控制面节点；世界节点与大厅都向它注册。
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

	"starve/internal/game/config"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	natsURL := config.EnvOr("STARVE_NATS_URL", "nats://127.0.0.1:4222")
	nodeID := config.EnvOr("STARVE_NODE_ID", "registry")
	// 对外公布的地址仅用于 roster 展示；NATS 模式中心无需本地数据监听。
	advertise := config.EnvOr("STARVE_REGISTRY_ADDR", "registry:8081")

	engine := actor.NewEngine(actor.Config{
		Cluster: actor.ClusterConfig{
			IsRegistry: true,
			NodeAddr:   advertise,
			NodeID:     nodeID,
			Service: cluster.NewNATSServiceCenter(actor.ClusterConfig{
				NodeAddr: advertise,
				NodeID:   nodeID,
				LeaseTTL: 9 * time.Second,
			}, natsURL),
		},
	})
	defer engine.Shutdown()
	if err := engine.StartCluster(ctx); err != nil {
		slog.Error("registry start", "err", err)
		os.Exit(1)
	}
	slog.Info("registry ready", "node", nodeID, "nats", natsURL)
	<-ctx.Done()
}
