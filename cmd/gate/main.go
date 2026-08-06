// 网关进程入口：WS + pomelo + 世界 actor + Gateway（M4 最小闭环）。
// 运行：go run ./cmd/gate（默认 ws://localhost:8081/ws）
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TheChosenGay/combet"
	"github.com/TheChosenGay/combet/ws"

	"starve/internal/actor"
	"starve/internal/game/world"
	"starve/internal/gateway"
	"starve/internal/gateway/pomelo"
)

func main() {
	addr := envOr("GATE_WS_ADDR", ":8081")

	engine := actor.NewEngine(actor.Config{})
	defer engine.Shutdown()

	// 世界：10Hz 自驱动，位置广播
	wa := world.NewWorldActor(world.WorldConfig{
		TickInterval:       100 * time.Millisecond,
		BroadcastPositions: true,
	})
	worldPID := engine.Spawn(func() actor.IActor { return wa }, "world", "room-1")
	engine.Send(worldPID, world.Start{})

	// 网关：combet Core + pomeloScheme + Gateway（Business/HandshakeHandler）
	gw := gateway.NewGateway(engine, worldPID)
	core := comet.NewCore(comet.ServerConfig{
		Business: gw,
		Scheme:   pomelo.NewScheme(),
	})
	gw.AttachCore(core)
	wa.SetPushSink(gw.HandlePush)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("starve gate listening on ws://localhost%s/ws", addr)
	if err := ws.NewServerWithCore(addr, core).Start(ctx); err != nil {
		log.Fatalf("ws serve: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
