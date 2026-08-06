package actor

import (
	"sync"
	"testing"
	"time"
)

// TestShutdownDrainsPendingMessages：Shutdown 用毒药收尾——
// 毒药排在邮箱末尾，之前排队的消息先全部处理完，再退出。
func TestShutdownDrainsPendingMessages(t *testing.T) {
	e := NewEngine(Config{})
	a := &collectActor{}
	pid := e.Spawn(func() IActor { return a }, "world", "room-1")
	for i := 0; i < 5; i++ {
		e.Send(pid, i)
	}
	e.Shutdown()

	a.waitCount(5, t)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.msgs) != 5 {
		t.Fatalf("drained %d messages, want 5", len(a.msgs))
	}
}

// TestPoisonDrainsThenStops：Poison 后排在毒药前的消息照常处理，
// 毒药后的消息被丢弃（邮箱已关闭）。
func TestPoisonDrainsThenStops(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	a := &collectActor{}
	pid := e.Spawn(func() IActor { return a }, "world", "room-1")

	e.Send(pid, 1)
	e.Send(pid, 2)
	e.Poison(pid)
	a.waitCount(2, t)

	e.Send(pid, 3) // 毒药已关闭邮箱 → dead letter
	time.Sleep(30 * time.Millisecond)

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.msgs) != 2 {
		t.Fatalf("got %d messages, want 2（毒药后消息应被丢弃）", len(a.msgs))
	}
}

// counterActor 统计收到的 int 消息数量，便于断言"排干"。
type counterActor struct {
	mu sync.Mutex
	n  int
}

func (a *counterActor) Receive(ctx IActorContext) {
	switch ctx.Message().(type) {
	case int:
		a.mu.Lock()
		a.n++
		a.mu.Unlock()
	}
}

func (a *counterActor) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

func (a *counterActor) waitCount(n int, t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if a.count() >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("count = %d, want %d", a.count(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// spawnCounter 收到 spawnChildMsg 时派生子 actor（固定实例便于断言）。
type spawnCounter struct {
	child *counterActor
}

func (a *spawnCounter) Receive(ctx IActorContext) {
	switch m := ctx.Message().(type) {
	case spawnChildMsg:
		ctx.SpawnChild(func() IActor { return a.child }, m.name)
	}
}

// TestPoisonPropagatesToChildren：毒药沿父子树传播——父先毒子（子排干后关），
// 父最后关闭自己；Shutdown 只需毒顶层。
func TestPoisonPropagatesToChildren(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()

	child := &counterActor{}
	parentPID := e.Spawn(func() IActor { return &spawnCounter{child: child} }, "world", "room-1")
	e.Send(parentPID, spawnChildMsg{name: "camera"})

	// 等子 actor 注册
	childPID := waitRegisteredPID(t, e, "world", "room-1.camera")

	e.Send(childPID, 1)
	e.Send(childPID, 2)
	e.Poison(parentPID)

	// 子 actor 先排干自己的消息，再被毒死；父子邮箱最终都关闭
	child.waitCount(2, t)
	if !isMailboxClosed(e, childPID) {
		t.Fatal("child mailbox not closed after parent poison")
	}
	if !isMailboxClosed(e, parentPID) {
		t.Fatal("parent mailbox not closed after poison")
	}
}

func waitRegisteredPID(t *testing.T, e *Engine, kind, name string) *PID {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		if pid, ok := e.GetPid(kind, name); ok {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %s/%s not registered", kind, name)
		}
		time.Sleep(time.Millisecond)
	}
}

func isMailboxClosed(e *Engine, pid *PID) bool {
	p := e.lookup(pid)
	if p == nil {
		return true
	}
	select {
	case <-p.mailbox.closedCh:
		return true
	default:
		return false
	}
}
