package main

import (
	"fmt"
	"time"
)

// runInteractive 是 TUI 主循环：单线程持有世界状态，
// 网络推送与按键都从 channel 进来，渲染按固定节拍 —— 没有锁，也没有并发写状态。
func runInteractive(cli *client, vw, vh int, noColor, holdWalk bool, maxDuration time.Duration) error {
	t, err := openTerm()
	if err != nil {
		return err
	}
	defer t.restore()
	t.enter()

	keys := make(chan keyEvent, 64)
	go t.readKeys(keys)

	overlay := false
	cam := &camera{}
	status := ""
	statusUntil := time.Time{}

	mv := &mover{hold: holdWalk}
	pred := newPredictor()
	var lastFrame time.Time

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
		cam.follow(cx, cy, cols, rows-2)
		drawWorld(f, cli.world, cam.view(cols, rows-2), overlay)
		msg := status
		if msg != "" && time.Now().After(statusUntil) {
			msg, status = "", ""
		}
		px, py := pred.Position()
		drawHUD(f, cli.world, hudState{
			overlay: overlay, cam: cam.mode, facing: mv.facing,
			walking:    mv.walking(time.Now()),
			status:     msg,
			predicting: pred.active,
			predX:      px, predY: py,
			predErr:   pred.lastErr,
			predCorr:  pred.corrections,
			predSnaps: pred.snaps,
		})
		fmt.Print(f.render(noColor))
	}

	// 先等世界就绪再进循环（最多 8s；断线/超时会直接返回错误）
	if err := cli.waitReady(8 * time.Second); err != nil {
		return err
	}
	// waitReady 自己消费了那批推送（含登录快照），所以这里必须补一次 Sync：
	// 否则预测器要等到"下一个增量推送"才激活——而玩家站着不动时可能很久都没有增量，
	// 表现为 HUD 一直不显示预测、按方向键也不动（本地预测没启动）。
	pred.Sync(cli.world)
	if own := cli.world.ownEntity(); own != nil && own.pos != nil {
		pred.Reconcile(own.renderX(), own.renderY(), true)
	}
	draw()

	for {
		select {
		case k := <-keys:
			switch k.action {
			case keyQuit:
				return nil
			case keyMove:
				d := mv.press(k.dx, k.dy, time.Now(), movePulse)
				pred.SetIntent(d[0], d[1])
				if err := cli.sendMove(d[0], d[1]); err != nil {
					warn("发送失败: " + err.Error())
				}
			case keyStop:
				d := mv.release()
				pred.SetIntent(0, 0)
				if err := cli.sendMove(d[0], d[1]); err != nil {
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
			case keyToggleCamera:
				cam.toggle()
			case keyRedraw, keyNone:
			}
			draw()
		case m := <-cli.push:
			cli.applyPush(m)
			pred.Sync(cli.world)
			if own := cli.world.ownEntity(); own != nil && own.pos != nil {
				stopped := own.moveable != nil && own.moveable.DirX == 0 && own.moveable.DirY == 0
				pred.Reconcile(own.renderX(), own.renderY(), stopped)
			}
		case err := <-cli.fail:
			return err
		case <-ticker.C:
			// 脉冲结束 → 主动发停止（服务端是方向保持，不发就会一直走）
			if mv.tick(time.Now()) {
				pred.SetIntent(0, 0)
				if err := cli.sendMove(0, 0); err != nil {
					warn("发送失败: " + err.Error())
				}
			}
			// 本地预测推进：按真实帧间隔推进，与服务端 tick 时长解耦
			now := time.Now()
			if !lastFrame.IsZero() {
				pred.Tick(now.Sub(lastFrame).Seconds())
			}
			lastFrame = now
			// 把预测位置交给渲染层（预测跑在快照前面，肉眼看得到"领先"）。
			px2, py2 := pred.Position()
			cli.world.predX, cli.world.predY = px2, py2
			cli.world.predActive = pred.active
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

// movePulse 是一次按键对应的移动时长：10 格/秒 × 0.18s ≈ 2 格。
// 按住的自动重复会不断续期，所以"按住 = 连续走"。
const movePulse = 180 * time.Millisecond
