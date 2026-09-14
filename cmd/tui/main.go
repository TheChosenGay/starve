// Command tui 是终端里的最小可玩客户端：不开 Godot 也能连服务器走路、看世界、看碰撞体。
//
// 用途：开发期快速验证服务端（尤其是移动/寻路/避障/碰撞）——启动 < 0.2s、
// 在终端里跑、不依赖图形环境。
//
//	go run ./cmd/tui -uid 42          # 交互式：WASD/hjkl 移动，f 自动行为，c 碰撞体叠加
//	go run ./cmd/tui -dump            # 连上、收一帧快照+配置、打印一屏就退出（不需要 TTY，可进 CI）
//
// 协议与 tools/pomelo-client 完全一致：握手 → ack → gate.login →
// 收 world.snapshot / world.snapshot.delta / world.config；输入走 world.player.* 的 notify。
//
// 渲染是"每格一个字符"的俯视图（不是等距），所以它看的是**逻辑格坐标**，
// 与服务端寻路/占位/形状三层语义一一对应——核对碰撞体压没压住树，比 3D 里看更直接。
package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", "ws://localhost:8081/ws", "网关 WS 地址")
	uid := flag.String("uid", "42", "用户 ID（默认按 uid 自签 dev token）")
	token := flag.String("token", "", "显式登录 token（feeds user 服务签发；空则按 uid 自签 dev token）")
	access := flag.String("access", "", "房间 accessToken（大厅 POST /rooms 返回；本地单世界可留空）")
	viewW := flag.Int("w", 0, "视口宽（格）；0 = 按终端宽度自适应")
	viewH := flag.Int("h", 0, "视口高（格）；0 = 按终端高度自适应")
	dump := flag.Bool("dump", false, "连上收一帧就打印并退出（不需要 TTY）")
	wait := flag.Duration("wait", 8*time.Second, "-dump 的最长等待时间")
	duration := flag.Duration("duration", 0, "最长运行时间；0 = 直到按 q 退出")
	noColor := flag.Bool("no-color", false, "关闭颜色（管道/日志里更干净）")
	flag.Parse()

	cli, err := dial(*addr, *uid, *token, *access)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
	defer cli.close()
	fmt.Fprintf(os.Stderr, "已登录 %s：uid=%s entity=%d\n", *addr, cli.uid, cli.entity)

	if *dump {
		if err := cli.waitReady(*wait); err != nil {
			fmt.Fprintln(os.Stderr, "tui:", err)
			os.Exit(1)
		}
		cli.world.setOwn(cli.entity)
		renderDump(cli.world, *viewW, *viewH, *noColor)
		return
	}

	if err := runInteractive(cli, *viewW, *viewH, *noColor, *duration); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}
