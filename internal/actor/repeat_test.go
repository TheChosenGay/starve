package actor

import (
	"runtime"
	"testing"
	"time"
)

type startRepeatMsg struct {
	msg      any
	interval time.Duration
}

// repeatActor 收到 startRepeatMsg 后用 ctx.SendRepeat 定时给 target 发消息，
// 创建完成后关 ready（保证测试拿到 repeater 无竞态）。
type repeatActor struct {
	target *PID
	ready  chan struct{}
	rep    ISendRepeater
}

func (a *repeatActor) Receive(ctx IActorContext) {
	switch m := ctx.Message().(type) {
	case startRepeatMsg:
		a.rep = ctx.SendRepeat(a.target, m.msg, m.interval)
		close(a.ready)
	}
}

// TestSendRepeatStopsOnStop：Stop 之后不再发送
func TestSendRepeatStopsOnStop(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	counter := &counterActor{}
	counterPID := e.Spawn(func() IActor { return counter }, "bench", "counter")
	ready := make(chan struct{})
	repActor := &repeatActor{target: counterPID, ready: ready}
	pid := e.Spawn(func() IActor { return repActor }, "bench", "rep")

	e.Send(pid, startRepeatMsg{msg: 1, interval: 10 * time.Millisecond})
	<-ready
	counter.waitCount(3, t) // 等到至少 3 次

	repActor.rep.Stop()
	n := counter.count()
	time.Sleep(50 * time.Millisecond)
	if counter.count() != n {
		t.Fatalf("repeater still sending after Stop: %d -> %d", n, counter.count())
	}
}

// TestRepeaterStoppedOnShutdown：Shutdown 后 repeater goroutine 不泄漏
func TestRepeaterStoppedOnShutdown(t *testing.T) {
	runtime.GC()
	base := runtime.NumGoroutine()

	e := NewEngine(Config{})
	counter := &counterActor{}
	counterPID := e.Spawn(func() IActor { return counter }, "bench", "counter")
	ready := make(chan struct{})
	pid := e.Spawn(func() IActor { return &repeatActor{target: counterPID, ready: ready} }, "bench", "rep")

	e.Send(pid, startRepeatMsg{msg: 1, interval: time.Millisecond})
	<-ready
	counter.waitCount(1, t)
	e.Shutdown()

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

// TestBroadcastEvent：广播发给所有已注册 actor（含未启动的），每人一份
func TestBroadcastEvent(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	a1 := &collectActor{}
	a2 := &collectActor{}
	e.Spawn(func() IActor { return a1 }, "bench", "a1")
	e.Spawn(func() IActor { return a2 }, "bench", "a2")

	e.BroadcastEvent("event")
	a1.waitCount(1, t)
	a2.waitCount(1, t)

	a1.mu.Lock()
	defer a1.mu.Unlock()
	if len(a1.msgs) != 1 || a1.msgs[0] != "event" {
		t.Fatalf("a1 = %v", a1.msgs)
	}
}
