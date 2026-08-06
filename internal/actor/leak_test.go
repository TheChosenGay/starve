package actor

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// treeSpawner 收到 spawnTreeMsg 时按 level 递归派生子 actor：
// level>0 时生 branch 个孩子并各发一条 spawnTreeMsg（下一层继续生）。
type treeSpawner struct {
	level int
}

type spawnTreeMsg struct {
	branch int
}

func (a *treeSpawner) Receive(ctx IActorContext) {
	switch m := ctx.Message().(type) {
	case spawnTreeMsg:
		if a.level <= 0 {
			return
		}
		lv := a.level - 1
		for i := 0; i < m.branch; i++ {
			name := fmt.Sprintf("l%d-%d", a.level, i)
			child := ctx.SpawnChild(func() IActor { return &treeSpawner{level: lv} }, name)
			ctx.Send(child, spawnTreeMsg{branch: m.branch})
		}
	}
}

func waitPIDCount(t *testing.T, e *Engine, kind string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := len(e.GetPids(kind)); got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pids(%s) = %d, want %d", kind, len(e.GetPids(kind)), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestShutdownNoGoroutineLeak：复杂父子树 + 并发流量下，
// Shutdown 结束后所有 actor/投毒 goroutine 必须退出，goroutine 数回到基线。
func TestShutdownNoGoroutineLeak(t *testing.T) {
	runtime.GC()
	base := runtime.NumGoroutine()

	e := NewEngine(Config{})
	rootPID := e.Spawn(func() IActor { return &treeSpawner{level: 2} }, "tree", "root")

	// 并发流量：持续向树里随机节点发消息，直到 Shutdown 结束
	trafficDone := make(chan struct{})
	var traffic sync.WaitGroup
	for i := 0; i < 4; i++ {
		traffic.Add(1)
		go func(i int) {
			defer traffic.Done()
			for {
				select {
				case <-trafficDone:
					return
				default:
				}
				if pids := e.GetPids("tree"); len(pids) > 0 {
					e.Send(pids[i%len(pids)], i)
				}
			}
		}(i)
	}

	// 建树：root(level2) → 3 个孩子(level1) → 9 个孙子(level0)，共 13 个 process
	e.Send(rootPID, spawnTreeMsg{branch: 3})
	waitPIDCount(t, e, "tree", 13)
	time.Sleep(30 * time.Millisecond) // 让流量跑一会儿，留一些在途消息

	e.Shutdown()
	close(trafficDone)
	traffic.Wait()

	// 轮询等待 goroutine 回到基线（容忍少量运行时 jitter）
	deadline := time.Now().Add(3 * time.Second)
	for {
		runtime.GC()
		if n := runtime.NumGoroutine(); n <= base+5 {
			return
		} else if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: base=%d after=%d", base, n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
