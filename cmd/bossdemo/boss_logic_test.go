package main

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 本文件在**宿主机**验证 boss.html 背后的逻辑：跑真实 ECS + 真实行为树，
// 断言 Boss 的三阶段行为。前端只是把这里的结果画出来，所以逻辑对了，
// 浏览器里看到的就是对的。

// run 推进 n tick 并返回快照。
func run(w *bossWorld, n int) Snapshot {
	var snap Snapshot
	for i := 0; i < n; i++ {
		w.step()
		snap = w.snapshot()
	}
	return snap
}

// 阶段一：Boss 应投炸弹，不打拳。
func TestDemoPhaseOneThrowsBombs(t *testing.T) {
	w := newBossWorld()
	run(w, 40) // 2 秒

	if w.bossState().Phase != 0 {
		t.Fatalf("满血应停在阶段一: phase=%d", w.bossState().Phase)
	}
	bombs, punches := 0, 0
	for _, e := range w.events {
		switch e.Kind {
		case "bomb":
			bombs++
		case "punch":
			punches++
		}
	}
	if bombs == 0 {
		t.Fatalf("阶段一应投弹: bombs=%d events=%v", bombs, w.events)
	}
	if punches != 0 {
		t.Fatalf("阶段一不该打拳: punches=%d", punches)
	}
}

// 掉血过半 → 进入阶段二 → 嚎叫一次 → 闪现。
func TestDemoEntersPhaseTwo(t *testing.T) {
	w := newBossWorld()
	run(w, 10)
	// 打到阈值以下
	w.damageBoss(demoBossHP - demoPhase2HP + 1)
	if w.bossState().HP > demoPhase2HP {
		t.Fatalf("前置条件：血量应低于阈值: hp=%d", w.bossState().HP)
	}

	run(w, 120) // 6 秒，足够走完嚎叫（1.5s）+ 闪现

	if w.bossState().Phase != 2 {
		t.Fatalf("血量过半应进入阶段二: phase=%d hp=%d", w.bossState().Phase, w.bossState().HP)
	}
	roars, leaps := 0, 0
	for _, e := range w.events {
		switch e.Kind {
		case "roar":
			roars++
		case "leap":
			leaps++
		}
	}
	if roars != 1 {
		t.Fatalf("嚎叫应只发生一次: roars=%d", roars)
	}
	if leaps == 0 {
		t.Fatalf("嚎叫后应闪现贴脸: leaps=%d", leaps)
	}
}

// 阶段二三拳一砸：应能观察到锤地 AOE。
func TestDemoPunchAndSlam(t *testing.T) {
	w := newBossWorld()
	run(w, 10)
	w.damageBoss(demoBossHP - demoPhase2HP + 1)
	// 把玩家放到 Boss 身边，确保"贴脸"分支生效
	w.movePlayer(1, 0)

	run(w, 200) // 10 秒

	slams := 0
	for _, e := range w.events {
		if e.Kind == "slam" {
			slams++
		}
	}
	if slams == 0 {
		t.Fatalf("三拳后应锤地: slams=%d events=%v", slams, w.events)
	}
	// 锤地应对身边的玩家造成伤害
	if w.playerHP() >= 500 {
		t.Fatalf("锤地 AOE 应命中贴脸的玩家: hp=%d", w.playerHP())
	}
}

// 闪现应真的把 Boss 挪到玩家身边（验证 LeapTo 是"真位移"）。
//
// 时序：阶段二先嚎叫（约 30 tick），嚎叫结束才闪现。所以这里逐 tick 观察，
// 断言"闪现那一刻距离骤降"，而不是按固定 tick 数取快照——后者会错过时机。
func TestDemoLeapActuallyMoves(t *testing.T) {
	w := newBossWorld()
	run(w, 10)
	w.damageBoss(demoBossHP - demoPhase2HP + 1)
	// 玩家放远处，便于观察"闪现前后距离骤减"
	w.movePlayer(12, 12)

	var before, after float64
	sawLeap := false
	for i := 0; i < 120; i++ {
		bx, by := w.bossPos()
		px, py := w.playerPos()
		prev := dist2(bx, by, px, py)
		w.step()
		s := w.snapshot()
		if s.LastAct == "leap" && !sawLeap {
			sawLeap = true
			before = prev
			bx, by = w.bossPos()
			px, py = w.playerPos()
			after = dist2(bx, by, px, py)
			break
		}
	}
	if !sawLeap {
		t.Fatalf("阶段二应发生闪现")
	}
	if after >= before {
		t.Fatalf("闪现应显著拉近距离: before=%.1f after=%.1f", before, after)
	}
	if after > 3 {
		t.Fatalf("闪现后应贴脸（距离 <= 3）: %.1f", after)
	}
}

// 闪现必须只发生一次：落地后若仍判定"没贴脸"，会陷入每 tick 闪烁的 bug。
//
// 这是真实踩过的坑——LeapTo 落在相邻格（对角曼哈顿距离 2），
// 而 MeleeRange 配成 1 时永远不满足"已贴脸"，Boss 就在原地疯狂闪现、
// 一次拳都打不出来。测试守住"闪现次数有界"。
func TestDemoLeapDoesNotRepeat(t *testing.T) {
	w := newBossWorld()
	run(w, 10)
	w.damageBoss(demoBossHP - demoPhase2HP + 1)
	w.movePlayer(12, 12)

	leaps := 0
	for i := 0; i < 200; i++ {
		w.step()
		if w.snapshot().LastAct == "leap" {
			leaps++
		}
	}
	if leaps > 2 {
		t.Fatalf("闪现不该反复发生: leaps=%d（说明落地后仍未判定为贴脸）", leaps)
	}
	if leaps == 0 {
		t.Fatalf("应至少闪现一次")
	}
}

// 阶段不会回退。
func TestDemoPhaseSticky(t *testing.T) {
	w := newBossWorld()
	run(w, 10)
	w.damageBoss(demoBossHP - demoPhase2HP + 1)
	run(w, 60)
	if w.bossState().Phase != 2 {
		t.Fatalf("应进入阶段二")
	}
	// 治疗回满
	w.healBoss(demoBossHP)
	run(w, 20)
	if w.bossState().Phase != 2 {
		t.Fatalf("阶段不应回退: phase=%d", w.bossState().Phase)
	}
}

// 快照必须可序列化且字段合理（前端依赖它渲染）。
func TestDemoSnapshotSane(t *testing.T) {
	w := newBossWorld()
	snap := run(w, 50)
	if snap.Boss.MaxHP != demoBossHP {
		t.Fatalf("MaxHP 错误: %d", snap.Boss.MaxHP)
	}
	if snap.Boss.HP <= 0 {
		t.Fatalf("Boss 不该在演示中死亡: hp=%d", snap.Boss.HP)
	}
	if snap.Tick != 50 {
		t.Fatalf("tick 应为 50: %d", snap.Tick)
	}
	if snap.Player.HP <= 0 {
		t.Fatalf("玩家不该死亡: hp=%d", snap.Player.HP)
	}
}

// 确定性：同样操作跑两次，结果一致（行为树 + ECS 都要求可复现）。
func TestDemoDeterministic(t *testing.T) {
	runOnce := func() []int {
		w := newBossWorld()
		var out []int
		for i := 0; i < 120; i++ {
			w.step()
			if i == 10 {
				w.damageBoss(demoBossHP - demoPhase2HP + 1)
				w.movePlayer(1, 0)
			}
			s := w.snapshot()
			out = append(out, s.Boss.HP*1000+int(s.Boss.X)*10+int(s.Boss.Y)+s.Boss.Phase)
		}
		return out
	}
	a, b := runOnce(), runOnce()
	if len(a) != len(b) {
		t.Fatalf("长度不同: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("第 %d tick 结果不一致: %d vs %d", i, a[i], b[i])
		}
	}
}

// --- 测试辅助 ---

func (w *bossWorld) bossState() BossState { return w.snapshot().Boss }
func (w *bossWorld) bossPos() (float64, float64) {
	s := w.snapshot().Boss
	return s.X, s.Y
}
func (w *bossWorld) playerPos() (float64, float64) {
	s := w.snapshot().Player
	return s.X, s.Y
}
func (w *bossWorld) playerHP() int { return w.snapshot().Player.HP }

// healBoss 把 Boss 血量设回指定值（验证"阶段不回退"）。
func (w *bossWorld) healBoss(v int) {
	if !w.sim.IsAlive(w.boss) {
		return
	}
	hp := ecs.Get[components.Health](w.sim, w.boss)
	hp.Cur = v
	ecs.MarkDirty[components.Health](w.sim, w.boss)
}

// dist2 两点距离（测试用）。
func dist2(x1, y1, x2, y2 float64) float64 {
	return math.Hypot(x1-x2, y1-y2)
}
