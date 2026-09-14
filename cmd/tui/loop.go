package main

import (
	"fmt"
	"time"
)

// runInteractive 是 TUI 主循环：单线程持有世界状态，
// 网络推送与按键都从 channel 进来，渲染按固定节拍 —— 没有锁，也没有并发写状态。
func runInteractive(cli *client, vw, vh int, noColor bool, maxDuration time.Duration) error {
	t, err := openTerm()
	if err != nil {
		return err
	}
	defer t.restore()
	t.enter()

	keys := make(chan keyEvent, 64)
	go t.readKeys(keys)

	overlay := false
	status := ""
	statusUntil := time.Time{}

	ticker := time.NewTicker(100 * time.Millisecond) // 10Hz：够跟手，也不刷屏
	defer ticker.Stop()
	var deadline <-chan time.Time
	if maxDuration > 0 {
		ch := time.After(maxDuration)
		deadline = ch
	}

	warn := func(msg string) {
		status, statusUntil = msg, time.Now().Add(3*time.Second)
	}

	draw := func() {
		t.refreshSize()
		cols, rows := t.cols, t.rows
		if vw > 0 {
			cols = vw
		}
		if vh > 0 {
			rows = vh
		}
		f := newFrame(cols, rows)
		cx, cy := w2cli(cli)
		drawWorld(f, cli.world, centerView(cx, cy, cols, rows-2), overlay)
		msg := status
		if msg != "" && time.Now().After(statusUntil) {
			msg, status = "", ""
		}
		drawHUD(f, cli.world, overlay, msg)
		fmt.Print(f.render(noColor))
	}

	// 先等世界就绪再进循环（最多 8s；断线/超时会直接返回错误）
	if err := cli.waitReady(8 * time.Second); err != nil {
		return err
	}
	draw()

	for {
		select {
		case k := <-keys:
			switch k.action {
			case keyQuit:
				return nil
			case keyMove:
				if err := cli.sendMove(k.dx, k.dy); err != nil {
					warn("发送失败: " + err.Error())
				}
			case keyStop:
				if err := cli.sendMove(0, 0); err != nil {
					warn("发送失败: " + err.Error())
				}
			case keyAutomate:
				if err := cli.sendAutomate(); err != nil {
					warn("发送失败: " + err.Error())
				}
			case keyWork:
				if msg := doWork(cli); msg != "" {
					warn(msg)
				}
			case keyPickup:
				if msg := doPickup(cli); msg != "" {
					warn(msg)
				}
			case keyToggleOverlay:
				overlay = !overlay
			case keyRedraw, keyNone:
			}
			draw()
		case m := <-cli.push:
			cli.applyPush(m)
		case err := <-cli.fail:
			return err
		case <-ticker.C:
			draw()
		case <-deadline:
			return nil
		}
	}
}

// w2cli 返回视口中心：优先跟随自己的实体，其次地图中心。
func w2cli(cli *client) (int, int) {
	if own := cli.world.ownEntity(); own != nil && own.pos != nil {
		return own.tileX(), own.tileY()
	}
	return cli.world.width / 2, cli.world.height / 2
}

// doWork 对最近的可作业目标发对应的动作（砍/挖/采）。
func doWork(cli *client) string {
	own := cli.world.ownEntity()
	if own == nil || own.pos == nil {
		return "还没拿到自己的位置"
	}
	target := cli.world.nearbyWorkable(own.tileX(), own.tileY(), interactRadius)
	if target == nil {
		return fmt.Sprintf("附近 %d 格内没有可作业目标", interactRadius)
	}
	if err := cli.sendWork(target.id, target.workAction); err != nil {
		return "发送失败: " + err.Error()
	}
	name := cli.world.kindName(target.workKind)
	action := shortEnum(target.workAction.String())
	return fmt.Sprintf("%s → %s(%d)", action, name, target.id)
}

// doPickup 拾取最近的掉落物。
func doPickup(cli *client) string {
	own := cli.world.ownEntity()
	if own == nil || own.pos == nil {
		return "还没拿到自己的位置"
	}
	target := cli.world.nearbyLoot(own.tileX(), own.tileY(), interactRadius)
	if target == nil {
		return fmt.Sprintf("附近 %d 格内没有掉落物", interactRadius)
	}
	if err := cli.sendPickup(target.id); err != nil {
		return "发送失败: " + err.Error()
	}
	return fmt.Sprintf("拾取 → 实体 %d", target.id)
}

// interactRadius 是 e/p 键的作用半径（格）。
const interactRadius = 6
