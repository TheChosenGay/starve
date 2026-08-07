// cmd/replay 回放验收工具：加载存档 → 按指令日志从空世界重放 → 与存档快照对比。
//
// 用法：
//
//	go run ./cmd/replay -save data/save.bin -resources configs/resources.json
//
// 重放世界的配置必须与存档原始世界一致（资源 seed、tick、速率），
// 否则实体 ID 与系统推进会对不上。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/game/world"
	game "starve/pkg/proto/game"
)

func main() {
	savePath := flag.String("save", "data/save.bin", "存档文件")
	resources := flag.String("resources", "configs/resources.json", "资源配置表（需与存档原始世界一致；无资源传空串）")
	tickMS := flag.Int("tick-ms", 100, "模拟步长（毫秒）")
	hungerRate := flag.Int("hunger-rate", 0, "饥饿速率（0=不消耗）")
	offlineSeconds := flag.Int("offline-seconds", 300, "离线保留秒数")
	flag.Parse()

	data, err := os.ReadFile(*savePath)
	if err != nil {
		log.Fatalf("读取存档: %v", err)
	}
	var sd game.SaveData
	if err := pb.Unmarshal(data, &sd); err != nil {
		log.Fatalf("解析存档: %v", err)
	}
	var entries []world.JournalEntry
	if err := json.Unmarshal(sd.Journal, &entries); err != nil {
		log.Fatalf("解析指令日志: %v", err)
	}

	cfg := world.WorldConfig{
		TickInterval:          time.Duration(*tickMS) * time.Millisecond,
		HungerRate:            *hungerRate,
		OfflineRetentionTicks: *offlineSeconds * 1000 / *tickMS,
		ResourcesPath:         *resources,
	}
	replayed, err := world.ReplaySave(data, cfg)
	if err != nil {
		log.Fatalf("重放失败: %v", err)
	}

	if !pb.Equal(replayed, sd.Snapshot) {
		fmt.Println("DIFF: 重放结果与存档快照不一致")
		os.Exit(1)
	}
	fmt.Printf("OK: 重放一致（tick=%d, 实体=%d, 日志=%d 条）\n",
		sd.Meta.Tick, len(sd.Snapshot.Entities), len(entries))
}
