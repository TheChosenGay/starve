package behavior

import "testing"

// 本文件验证 Boss 三阶段行为树：阶段切换、嚎叫一次、闪现贴脸、三拳一砸循环。
// 用 fakeBoard/fakeEnv（见 node_test.go）作为黑板与环境的替身。

// newBossHarness 组装 Boss 场景：指定血量与到目标的距离。
func newBossHarness(t *testing.T, cfg BossConfig, hp int, dist int) (*harness, *fakeBoard, *fakeEnv) {
	t.Helper()
	tree := BossTree(cfg)
	board := &fakeBoard{
		target:   99, // 有目标
		self:     7,
		hp:       hp,
		rng:      1,
		dmg:      20,
		phase2HP: cfg.Phase2HP,
		dist:     dist,
	}
	env := newFakeEnv()
	h := newHarness(t, tree, board, env)
	return h, board, env
}

// 阶段一：Boss 应投炸弹，而不是打拳或闪现。
func TestBossPhaseOneThrowsBombs(t *testing.T) {
	cfg := DefaultBossConfig()
	h, board, env := newBossHarness(t, cfg, 1000, 5) // 血高 = 阶段一

	h.tick()
	if env.bombs != 1 {
		t.Fatalf("阶段一应投弹: bombs=%d", env.bombs)
	}
	if env.punches != 0 || env.leaps != 0 || env.slamCount != 0 {
		t.Fatalf("阶段一不该打拳/闪现/锤地: punch=%d leap=%d slam=%d",
			env.punches, env.leaps, env.slamCount)
	}
	if board.phase != 0 {
		t.Fatalf("阶段一阶段值应保持 0: phase=%d", board.phase)
	}
}

// 阶段一：目标太远时，Boss 应**一边接近一边投弹**（不是只走不打）。
//
// 这条断言的是修正后的行为：早期版本"太远就只追击"，导致玩家走远后
// 炸弹完全停掉（演示里非常明显）。现在只要有目标就投弹，太远时额外接近。
func TestBossPhaseOneApproachesAndStillThrows(t *testing.T) {
	cfg := DefaultBossConfig()
	h, _, env := newBossHarness(t, cfg, 1000, cfg.ThrowRange+5)

	h.tick()
	if env.bombs != 1 {
		t.Fatalf("超出投弹距离也应投弹: bombs=%d", env.bombs)
	}
	if len(env.toward) == 0 {
		t.Fatalf("超出投弹距离应同时朝目标接近")
	}
}

// 阶段一：目标在投弹距离内时，只投弹、不移动。
func TestBossPhaseOneInRangeDoesNotChase(t *testing.T) {
	cfg := DefaultBossConfig()
	h, _, env := newBossHarness(t, cfg, 1000, 3) // 在 ThrowRange 内

	h.tick()
	if env.bombs != 1 {
		t.Fatalf("应投弹: bombs=%d", env.bombs)
	}
	if len(env.toward) != 0 {
		t.Fatalf("已在投弹距离内不该再移动: toward=%v", env.toward)
	}
}

// 阶段切换：血量掉到阈值以下，下一次 tick 应切到阶段二。
func TestBossEntersPhaseTwoOnLowHP(t *testing.T) {
	cfg := DefaultBossConfig()
	h, board, _ := newBossHarness(t, cfg, 1000, 5)

	h.tick()
	if board.phase != 0 {
		t.Fatalf("满血不该进阶段二: phase=%d", board.phase)
	}
	board.hp = cfg.Phase2HP - 1 // 掉到阈值以下
	h.tick()
	if board.phase != 2 {
		t.Fatalf("血量过半应进入阶段二: phase=%d hp=%d 阈值=%d", board.phase, board.hp, cfg.Phase2HP)
	}
}

// 阶段二完整时序：嚎叫一次 → 闪现贴脸 → 三拳 → 锤地 AOE。
func TestBossPhaseTwoFullSequence(t *testing.T) {
	cfg := DefaultBossConfig()
	cfg.RoarTicks = 3 // 缩短测试时长
	h, board, env := newBossHarness(t, cfg, 1000, 5)

	// 先切到阶段二
	board.hp = cfg.Phase2HP - 1
	h.tick()
	if board.phase != 2 {
		t.Fatalf("应进入阶段二: phase=%d", board.phase)
	}

	// ① 嚎叫：应在若干 tick 内触发一次，且期间是 Running
	roarSeen := false
	for i := 0; i < cfg.RoarTicks+2; i++ {
		if st := h.tick(); st == Running {
			roarSeen = true
		}
	}
	if !roarSeen {
		t.Fatalf("嚎叫应返回 Running（有持续时间）")
	}
	if env.roars != 1 {
		t.Fatalf("嚎叫应只触发一次: roars=%d", env.roars)
	}
	// ② 闪现：嚎叫结束后应闪现贴脸（距离远 → 触发 LeapTo）
	if env.leaps == 0 {
		t.Fatalf("嚎叫后应闪现到目标身边: leaps=%d", env.leaps)
	}
	// 闪现后距离视为已贴脸
	board.dist = 0

	// ③ 三拳 → 一砸
	h.tick() // 第 1 拳
	if env.punches != 1 {
		t.Fatalf("第 1 拳应打出: punches=%d", env.punches)
	}
	h.tick() // 第 2 拳
	h.tick() // 第 3 拳
	if env.punches != 3 {
		t.Fatalf("应打满 3 拳: punches=%d", env.punches)
	}
	if env.slamCount != 0 {
		t.Fatalf("三拳之前不该锤地: slam=%d", env.slamCount)
	}
	// 第 4 次进入连招 → 收招：锤地
	h.tick()
	if env.slamCount != 1 {
		t.Fatalf("三拳后应锤地: slam=%d punches=%d", env.slamCount, env.punches)
	}
	// AOE 有前摇：期间继续 Running，不重复触发
	for i := 0; i < cfg.SlamTicks; i++ {
		h.tick()
	}
	if env.slamCount != 1 {
		t.Fatalf("AOE 前摇期间不该重复触发: slam=%d", env.slamCount)
	}
}

// 三拳一砸应循环：砸完之后重新开始数三拳。
func TestBossPunchCycleRepeats(t *testing.T) {
	cfg := DefaultBossConfig()
	cfg.RoarTicks = 1
	cfg.SlamTicks = 1
	h, board, env := newBossHarness(t, cfg, 1000, 0) // 已贴脸
	board.phase = 2
	// 让 Once(嚎叫) 认为已经完成：找到 Once 节点的 id 并置位
	tree := BossTree(cfg)
	_ = tree

	// 直接跑足够多 tick，观察连招循环
	for i := 0; i < 40; i++ {
		h.tick()
	}
	if env.punches < 6 {
		t.Fatalf("连招应循环，至少打 6 拳: punches=%d", env.punches)
	}
	if env.slamCount < 2 {
		t.Fatalf("连招应循环，至少锤地 2 次: slam=%d", env.slamCount)
	}
}

// 阶段二没有目标时应回防，而不是空放技能。
func TestBossPhaseTwoNoTargetIdles(t *testing.T) {
	cfg := DefaultBossConfig()
	h, board, env := newBossHarness(t, cfg, 1000, 0)
	board.phase = 2
	board.target = 0

	h.tick()
	if env.leaps != 0 || env.punches != 0 || env.slamCount != 0 {
		t.Fatalf("无目标不该放技能: leap=%d punch=%d slam=%d", env.leaps, env.punches, env.slamCount)
	}
}

// 阶段一旦进入二阶段就不会退回（阶段是持久状态）。
func TestBossPhaseDoesNotRegress(t *testing.T) {
	cfg := DefaultBossConfig()
	h, board, _ := newBossHarness(t, cfg, 1000, 5)
	board.hp = cfg.Phase2HP - 1
	h.tick()
	if board.phase != 2 {
		t.Fatalf("应进入阶段二")
	}
	// 血量被治疗回满，阶段不应回退
	board.hp = 1000
	h.tick()
	if board.phase != 2 {
		t.Fatalf("阶段不应回退: phase=%d", board.phase)
	}
}

// Boss 树的 id 分配必须稳定（存档兼容）。
func TestBossTreeStableIDs(t *testing.T) {
	cfg := DefaultBossConfig()
	t1, t2 := BossTree(cfg), BossTree(cfg)
	var ids1, ids2 []NodeID
	var collect func(n Node, out *[]NodeID)
	collect = func(n Node, out *[]NodeID) {
		*out = append(*out, idOf(n))
		for _, c := range n.Children() {
			collect(c, out)
		}
	}
	collect(t1.Root(), &ids1)
	collect(t2.Root(), &ids2)
	if len(ids1) != len(ids2) {
		t.Fatalf("节点数不同: %d vs %d", len(ids1), len(ids2))
	}
	for i := range ids1 {
		if ids1[i] != ids2[i] {
			t.Fatalf("第 %d 个节点 id 不稳定: %d vs %d", i, ids1[i], ids2[i])
		}
	}
}

// Counter 装饰器语义单测：数够 N 次后改跑 after，并清零重来。
func TestCounterRunsAfterBranch(t *testing.T) {
	child := &countingNode{}
	after := &countingNode{}
	tree := NewTree(NewCounter(3, child, after))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())

	for i := 0; i < 3; i++ {
		h.tick()
	}
	if child.ticks != 3 || after.ticks != 0 {
		t.Fatalf("前 3 次应跑 child: child=%d after=%d", child.ticks, after.ticks)
	}
	h.tick() // 第 4 次 → after
	if after.ticks != 1 {
		t.Fatalf("第 4 次应跑 after: after=%d", after.ticks)
	}
	h.tick() // 清零后重新数
	if child.ticks != 4 {
		t.Fatalf("after 之后应重新数 child: child=%d", child.ticks)
	}
}

// Once 装饰器：成功一次后永不再执行。
func TestOnceRunsOnlyOnce(t *testing.T) {
	child := &countingNode{}
	tree := NewTree(NewOnce(child))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())

	if got := h.tick(); got != Success {
		t.Fatalf("首次应成功: %v", got)
	}
	if got := h.tick(); got != Failure {
		t.Fatalf("第二次应 Failure（已用过）: %v", got)
	}
	if child.ticks != 1 {
		t.Fatalf("子节点应只执行一次: %d", child.ticks)
	}
}

// Once 遇到 Running 不应消耗掉机会。
func TestOnceDoesNotConsumeOnRunning(t *testing.T) {
	child := &scriptedNode{results: []Status{Running, Success}}
	tree := NewTree(NewOnce(child))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())

	if got := h.tick(); got != Running {
		t.Fatalf("首次应 Running: %v", got)
	}
	if got := h.tick(); got != Success {
		t.Fatalf("Running 后应能继续并成功: %v", got)
	}
	if child.ticks != 2 {
		t.Fatalf("子节点应执行 2 次: %d", child.ticks)
	}
}
