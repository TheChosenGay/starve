// 网关进程入口：WS + pomelo + 世界 actor + Gateway（M4 最小闭环）。
// 运行：go run ./cmd/gate（默认 ws://localhost:8081/ws）
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
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
	tickMS := envOrInt("GATE_TICK_MS", 100)
	saveFile := envOr("GATE_SAVE_FILE", "data/save.bin")
	resourcesPath := envOr("GATE_RESOURCES", "configs/resources.json")

	engine := actor.NewEngine(actor.Config{})
	defer engine.Shutdown()

	// 世界：10Hz 自驱动，位置广播
	wa := world.NewWorldActor(world.WorldConfig{
		TickInterval:  time.Duration(tickMS) * time.Millisecond,
		ResourcesPath: resourcesPath,
	})
	// 启动前加载存档（若存在）
	if data, err := os.ReadFile(saveFile); err == nil {
		if err := wa.Load(data); err != nil {
			log.Fatalf("load save: %v", err)
		}
		log.Printf("world loaded from %s (tick=%d)", saveFile, wa.WorldTime())
	}
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
	// 事件触发存档 → 落盘（SaveNow）
	wa.SetSaveSink(func(data []byte) {
		if len(data) == 0 {
			return
		}
		if err := os.WriteFile(saveFile, data, 0o644); err != nil {
			log.Printf("event save failed: %v", err)
		} else {
			log.Printf("world saved on event to %s", saveFile)
		}
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("starve gate listening on ws://localhost%s/ws", addr)
	if err := ws.NewServerWithCore(addr, core).Start(ctx); err != nil {
		log.Fatalf("ws serve: %v", err)
	}

	// 关服前保存：经 SaveRequest（actor 消息，线性模型）
	resp := engine.Request(worldPID, world.SaveRequest{}, 5*time.Second)
	if v, err := resp.Wait(); err == nil {
		if data, ok := v.([]byte); ok && len(data) > 0 {
			if err := os.WriteFile(saveFile, data, 0o644); err != nil {
				log.Printf("save failed: %v", err)
			} else {
				log.Printf("world saved to %s", saveFile)
			}
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
