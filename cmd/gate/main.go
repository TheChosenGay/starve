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
	tickMS := envOrInt("GATE_TICK_MS", 50)
	saveFile := envOr("GATE_SAVE_FILE", "data/save.bin")
	resourcesPath := envOr("GATE_RESOURCES", "configs/resources.json")
	templatesPath := envOr("GATE_TEMPLATES", "configs/resource_templates.json")
	recipesPath := envOr("GATE_RECIPES", "configs/crafting.json")
	stationsPath := envOr("GATE_STATIONS", "configs/stations.json")
	mapPath := envOr("GATE_MAP", "configs/map.json")
	weatherPath := envOr("GATE_WEATHER", "configs/weather.json")
	mapSeed := envOrUint64("GATE_MAP_SEED", 42)
	hungerRate := envOrInt("GATE_HUNGER_RATE", 0)
	offlineSeconds := envOrInt("GATE_OFFLINE_SECONDS", 300)
	corpseSeconds := envOrInt("GATE_CORPSE_SECONDS", 60)
	inventorySlots := envOrInt("GATE_INVENTORY_SLOTS", 20)
	moveInterval := envOrInt("GATE_MOVE_INTERVAL", 1) // 步进间隔（tick/格），1 = 每 tick 走一格

	engine := actor.NewEngine(actor.Config{})
	defer engine.Shutdown()

	// 世界：20Hz 自驱动，位置广播
	cfg := world.WorldConfig{
		TickInterval:          time.Duration(tickMS) * time.Millisecond,
		HungerRate:            hungerRate,
		OfflineRetentionTicks: offlineSeconds * 1000 / tickMS,
		CorpseRetentionTicks:  corpseSeconds * 1000 / tickMS,
		InventorySlots:        inventorySlots,
		TemplatesPath:         templatesPath,
		RecipesPath:           recipesPath,
		StationsPath:          stationsPath,
		MapPath:               mapPath,
		WeatherPath:           weatherPath,
		MapSeed:               mapSeed,
		MoveInterval:          moveInterval,
	}
	if _, err := os.Stat(saveFile); err != nil {
		cfg.ResourcesPath = resourcesPath // 无存档：旧资源配置 seed（map 无存档时也会生成）
		cfg.StationsPath = stationsPath
	} else {
		cfg.MapPath = "" // 有存档：地形/实体从存档恢复，不重新生成
	}
	wa := world.NewWorldActor(cfg)
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

func envOrUint64(key string, def uint64) uint64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return def
}
