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
	"starve/internal/game/config"
	"starve/internal/game/world"
	"starve/internal/gateway"
	"starve/internal/gateway/pomelo"
)

func main() {
	addr := config.EnvOr("GATE_WS_ADDR", ":8081")
	saveFile := config.EnvOr("GATE_SAVE_FILE", "data/save.bin")

	// 配置集中管理：路径/运行时参数/环境变量全部走 ConfigManager
	cm := config.NewConfigManagerFromEnv()
	// 有存档：地形/实体/区域从存档恢复，不重新生成，也不 seed 旧资源/工作站
	if _, err := os.Stat(saveFile); err == nil {
		cm.SetPath(config.ConfigMap, "")
		cm.SetPath(config.ConfigResources, "")
		cm.SetPath(config.ConfigStations, "")
	}
	cfg := cm.WorldConfig()
	gc, err := cm.Load()
	if err != nil {
		log.Printf("load configs: %v", err)
	}

	engine := actor.NewEngine(actor.Config{})
	defer engine.Shutdown()

	// 世界：20Hz 自驱动，位置广播
	wa := world.NewWorldActorWithConfig(cfg, gc)
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
	// 心跳超时踢线：连接（含心跳/任意帧）静默超过该阈值即关闭，网关 sweeper 转离线。
	readTimeout := time.Duration(config.EnvOrInt("GATE_HEARTBEAT_TIMEOUT", 90)) * time.Second
	gw.StartSweeper(time.Second)
	defer gw.StopSweeper()
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
	srv := ws.NewServerWithCore(addr, core)
	srv.ReadTimeout = readTimeout
	if err := srv.Start(ctx); err != nil {
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
