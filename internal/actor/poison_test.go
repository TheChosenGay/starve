package actor

import (
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
