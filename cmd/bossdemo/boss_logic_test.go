package main

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/behavior"
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

// 回归：玩家走远之后，Boss 必须**继续投弹**（而不是停手）。
//
// 真实踩过的坑：原阶段一树是"太远就先接近、否则投弹"，玩家一超过
// ThrowRange 就切到纯追击、再也不投弹。用户反馈"移动一段后它不投弹了"。
func TestDemoKeepsBombingAtAnyDistance(t *testing.T) {
	for _, d := range []float64{6, 15, 25} {
		w := newBossWorld()
		run(w, 20)
		w.movePlayer(demoOrigin+d, demoOrigin+d)
		bombs := 0
		for i := 0; i < 100; i++ {
			w.step()
			if w.snapshot().LastAct == "bomb" {
				bombs++
			}
		}
		if bombs == 0 {
			t.Fatalf("距离 %.0f 格时应继续投弹: bombs=%d", d, bombs)
		}
	}
}

// 回归：玩家走到场地边缘（离 Boss 最远）也不能丢失目标。
//
// 真实踩过的坑：AOI 感知网格按 y*Width+x 索引，**负坐标会被直接跳过**，
// 于是玩家走到负坐标后 Boss 就"看不见"他了，站着不动。现在演示场地
// 全部使用正坐标（demoOrigin），并由 clampCoord 保证不会越界。
func TestDemoNeverLosesTargetAtFieldEdge(t *testing.T) {
	w := newBossWorld()
	run(w, 20)
	// 场地四角
	corners := [][2]float64{
		{demoOrigin - demoFieldHalf, demoOrigin - demoFieldHalf},
		{demoOrigin + demoFieldHalf, demoOrigin + demoFieldHalf},
		{demoOrigin - demoFieldHalf, demoOrigin + demoFieldHalf},
		{demoOrigin + demoFieldHalf, demoOrigin - demoFieldHalf},
	}
	for _, c := range corners {
		w.movePlayer(c[0], c[1])
		for i := 0; i < 30; i++ {
			w.step()
		}
		s := w.snapshot()
		if s.Boss.Target == 0 {
			t.Fatalf("角落 (%.0f,%.0f) 不该丢失目标: boss=(%.0f,%.0f) player=(%.0f,%.0f)",
				c[0], c[1], s.Boss.X, s.Boss.Y, s.Player.X, s.Player.Y)
		}
	}
}

// 回归：距离远时进入二阶段，必须能**闪现贴脸**（而不是原地干等）。
func TestDemoLeapsFromFarAway(t *testing.T) {
	w := newBossWorld()
	run(w, 20)
	// 放在场地上一个较远但合法（正坐标）的位置
	w.movePlayer(demoOrigin+15, demoOrigin+15)
	w.damageBoss(demoBossHP - demoPhase2HP + 1)

	sawLeap := false
	for i := 0; i < 240; i++ {
		w.step()
		if w.snapshot().LastAct == "leap" {
			sawLeap = true
		}
	}
	if !sawLeap {
		t.Fatalf("远距离进入二阶段应闪现贴脸")
	}
	s := w.snapshot()
	d := dist2(s.Boss.X, s.Boss.Y, s.Player.X, s.Player.Y)
	if d > 3 {
		t.Fatalf("闪现后应贴脸: dist=%.1f boss=(%.0f,%.0f) player=(%.0f,%.0f)",
			d, s.Boss.X, s.Boss.Y, s.Player.X, s.Player.Y)
	}
}

// 坐标夹取：任何输入都必须落在正坐标场地内（否则 AOI 会看不见）。
func TestDemoCoordClamp(t *testing.T) {
	w := newBossWorld()
	for _, c := range [][2]float64{{-100, -100}, {9999, 9999}, {0, 0}} {
		w.movePlayer(c[0], c[1])
		px, py := w.playerPos()
		if px < 0 || py < 0 {
			t.Fatalf("坐标必须为非负（AOI 网格索引要求）: got (%.0f,%.0f) from (%.0f,%.0f)",
				px, py, c[0], c[1])
		}
		if px > demoOrigin+demoFieldHalf || py > demoOrigin+demoFieldHalf {
			t.Fatalf("坐标超出场地: got (%.0f,%.0f)", px, py)
		}
	}
}

// 回归：三拳一砸的**节奏**必须正确——砸的前摇要能走完，而不是被打断。
//
// 真实踩过的坑：Counter 在进入 after 分支的当 tick 就把计数清零，而 after
// （锤地 AOE）返回 Running 有前摇；下一 tick 计数已是 0，于是又回去执行
// Punch，AOE 永远走不完。表现为 Boss 疯狂循环"三拳→砸一下立刻被打断"，
// 玩家几乎看不到 AOE，且玩家掉血异常少。
func TestDemoComboRhythm(t *testing.T) {
	w := newBossWorld()
	run(w, 20)
	w.movePlayer(demoOrigin+15, demoOrigin+15)
	w.damageBoss(demoBossHP - demoPhase2HP + 1)

	// 统计出拳：直接看行为树里 Counter 的计数。
	//
	// 不硬编码节点 id——树结构调整会让 id 变化（真实踩过：加了个 Once 后
	// Counter 的 id 从 17 变 16，测试静默读到了别的节点）。这里按**类型**
	// 在树里现查。
	punches := 0
	slams := 0
	counterID := findNodeID(t, components.TreeKindBoss, func(n behavior.Node) bool {
		_, ok := n.(*behavior.Counter)
		return ok
	})
	prev := 0
	for i := 0; i < 200; i++ {
		w.step()
		bt := ecs.Get[components.BehaviorTree](w.sim, w.boss)
		// Counter 计数在"砸"时归零，所以用"跨过 1..3 的上升沿"累计出拳数：
		// 计数每次从 0 涨到 3 代表打了三拳。
		cur := bt.Counters[uint32(counterID)]
		if cur > prev {
			punches++
		}
		prev = cur
		if w.snapshot().LastAct == "slam" {
			slams++
		}
	}
	if punches < 6 {
		t.Fatalf("200 tick 内应打出多轮连招: punches=%d", punches)
	}
	// 关键：砸次数不能远超"拳数/3"。修 bug 前是 slam:4/punch:0 这种畸形比例。
	if slams > punches {
		t.Fatalf("锤地次数不应超过出拳数（收招被打断的征兆）: slam=%d punch=%d", slams, punches)
	}
	if slams == 0 {
		t.Fatalf("应至少锤地一次")
	}
}

// findNodeID 按类型在指定内置树里查找节点 id（避免测试硬编码节点 id）。
func findNodeID(t *testing.T, kind components.BehaviorTreeKind, match func(behavior.Node) bool) behavior.NodeID {
	t.Helper()
	tree := components.TreeOf(kind)
	var found behavior.NodeID
	var walk func(n behavior.Node)
	walk = func(n behavior.Node) {
		if match(n) && found == 0 {
			type ider interface{ ID() behavior.NodeID }
			if x, ok := n.(ider); ok {
				found = x.ID()
			}
		}
		for _, c := range n.Children() {
			walk(c)
		}
	}
	walk(tree.Root())
	if found == 0 {
		t.Fatal("未在树里找到匹配的节点")
	}
	return found
}

// 回归：**无论当前距离多远，进入二阶段都必须闪现一次**。
//
// 按需求，二阶段的招牌动作是"嚎叫完跳到玩家面前"。早期实现的条件是
// "距离 > MeleeRange 才跳"，于是玩家本来就在身边时（演示默认开着自动跟随）
// 完全不闪现——看起来像"在原地游荡，没跳过来"。改用 Once 保证必定触发。
func TestDemoAlwaysLeapsOnPhaseTwoEntry(t *testing.T) {
	for _, d := range []float64{1, 2, 3, 6, 12, 15} {
		w := newBossWorld()
		run(w, 20)
		w.movePlayer(demoOrigin+d, demoOrigin)
		w.damageBoss(demoBossHP - demoPhase2HP + 1)

		// 跑足够长：阶段切换那一 tick 可能恰好在投弹冷却里，
		// 嚎叫要等冷却走完才开始（最长 ~1 秒），再加嚎叫本身 1.5 秒。
		//
		// 注意**逐 tick 统计 LastAct 的变化**，而不是最后去数 snapshot().Events：
		// 事件是 60 条上限的环形缓冲，跑久了早期的 roar/leap 会被挤掉，
		// 数出来的次数会偏少（实测因此误判为"没嚎叫"）。
		leaps, roars := 0, 0
		prevAct := ""
		for i := 0; i < 400; i++ {
			w.step()
			if a := w.snapshot().LastAct; a != "" && a != prevAct {
				switch a {
				case "leap":
					leaps++
				case "roar":
					roars++
				}
			}
			prevAct = w.snapshot().LastAct
		}
		if roars != 1 {
			t.Fatalf("距离 %.0f：嚎叫应恰好一次: roars=%d", d, roars)
		}
		if leaps != 1 {
			t.Fatalf("距离 %.0f：应恰好闪现一次（远近都要跳）: leaps=%d", d, leaps)
		}
		s := w.snapshot()
		if dist := dist2(s.Boss.X, s.Boss.Y, s.Player.X, s.Player.Y); dist > 3 {
			t.Fatalf("距离 %.0f：闪现后应贴脸: dist=%.1f", d, dist)
		}
	}
}

// 回归：事件流水必须**如实反映三拳一砸**（而不是只有 AOE）。
//
// 出拳原先完全不记流水，导致面板上只剩 slam，看起来像"只会锤地、
// 没有三次普攻"。现在每次出拳都发 punch 事件，按"3 拳 1 砸"成组出现。
func TestDemoLogShowsPunchPattern(t *testing.T) {
	w := newBossWorld()
	run(w, 20)
	w.movePlayer(demoOrigin+1, demoOrigin) // 贴身
	w.damageBoss(demoBossHP - demoPhase2HP + 1)

	// 逐 tick 收集事件（事件缓冲是 60 条环形，跑久了会挤掉早期条目）
	var kinds []string
	seen := 0
	for i := 0; i < 400; i++ {
		w.step()
		evs := w.snapshot().Events
		for ; seen < len(evs); seen++ {
			kinds = append(kinds, evs[seen].Kind)
		}
		// 环形缓冲回绕时重置游标
		if seen > len(evs) {
			seen = len(evs)
		}
	}

	punches, slams := 0, 0
	for _, k := range kinds {
		switch k {
		case "punch":
			punches++
		case "slam":
			slams++
		}
	}
	if punches == 0 {
		t.Fatal("出拳应当出现在事件流水里（否则面板只剩 AOE）")
	}
	if slams == 0 {
		t.Fatal("应有锤地事件")
	}
	// 三拳一砸：拳数应当显著多于砸数（大约 3:1）
	if punches < slams*2 {
		t.Fatalf("拳/砸比例不符合三拳一砸: punch=%d slam=%d", punches, slams)
	}
}

// 回归：炸弹爆炸不应覆盖 Boss 本 tick 的决策高亮。
//
// 踩过的坑：嚎叫那一 tick 恰好有炸弹引爆，"当前动作"被覆盖成"投掷炸弹"，
// 看起来像二阶段还在投弹。现在爆炸走 logQuiet，不抢 lastAct。
func TestDemoBombDoesNotClobberCurrentAction(t *testing.T) {
	w := newBossWorld()
	run(w, 20)
	w.movePlayer(demoOrigin+6, demoOrigin)
	w.damageBoss(demoBossHP - demoPhase2HP + 1)

	// 嚎叫期间"当前动作"必须是 roar，不能被炸弹引爆改写
	sawRoar := false
	for i := 0; i < 60; i++ {
		w.step()
		if a := w.snapshot().LastAct; a == "roar" {
			sawRoar = true
		} else if sawRoar && a == "bomb" {
			t.Fatalf("嚎叫期间当前动作被炸弹覆盖为 bomb（t=%d）", i)
		}
	}
	if !sawRoar {
		t.Fatal("应观察到 roar 作为当前动作")
	}
}
