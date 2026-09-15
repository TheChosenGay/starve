package behavior

import "testing"

// 回归：Counter 的收招分支带 Running 时不能被 child 抢占。
func TestCounterAfterBranchKeepsRunning(t *testing.T) {
	child := &countingNode{}
	after := &scriptedNode{results: []Status{Running, Running, Success}}
	tree := NewTree(NewCounter(2, child, after))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())

	h.tick() // 拳 1
	h.tick() // 拳 2 → 计数满
	h.tick() // 进入 after（Running）
	h.tick() // after 继续（Running）——不能回去打拳
	if child.ticks != 2 {
		t.Fatalf("收招 Running 期间不该回去执行 child: child=%d", child.ticks)
	}
	if after.ticks != 2 {
		t.Fatalf("after 应持续被驱动: after=%d", after.ticks)
	}
	h.tick() // after 完成（Success）→ 计数清零；本 tick 到此为止
	if child.ticks != 2 {
		t.Fatalf("收招完成那一 tick 不该再执行 child: child=%d", child.ticks)
	}
	h.tick() // 下一 tick：重新开始数，回到 child
	if child.ticks != 3 {
		t.Fatalf("收招结束后应重新开始数 child: child=%d", child.ticks)
	}
}
