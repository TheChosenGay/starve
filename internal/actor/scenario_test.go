package actor

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// ---- 场景测试：补足边界场景覆盖 ----

// blockActor 在 Receive 里阻塞（模拟慢/卡住的 actor），entered 在进入 Receive 时发出信号。
type blockActor struct {
	entered chan struct{}
	release chan struct{}
}

func (a *blockActor) Receive(IActorContext) {
	select {
	case a.entered <- struct{}{}:
	default:
	}
	<-a.release
}

// TestASendTimeoutOnFullMailbox：邮箱满时 ASend 限时超时（引擎层，非仅 mailbox 层）
func TestASendTimeoutOnFullMailbox(t *testing.T) {
	e := NewEngine(Config{MailboxSize: 1})
	defer e.Shutdown()
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	pid := e.Spawn(func() IActor {
		return &blockActor{entered: entered, release: release}
	}, "world", "room-1")

	e.Send(pid, "first")
	<-entered             // actor 已阻塞在 Receive
	e.Send(pid, "second") // 填满邮箱

	err := e.ASend(pid, "third", 50*time.Millisecond)
	if !errors.Is(err, ErrMailboxTimeout) {
		t.Fatalf("err = %v, want ErrMailboxTimeout", err)
	}
	close(release)
}

type slowActor struct {
	release chan struct{}
}

func (a *slowActor) Receive(ctx IActorContext) {
	switch ctx.Message().(type) {
	case ping:
		<-a.release
		ctx.Respond(pong{})
	}
}

// TestLateReplyDropped：超时后目标才 Respond → 迟到回复被丢弃，不 panic
func TestLateReplyDropped(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	release := make(chan struct{})
	pid := e.Spawn(func() IActor { return &slowActor{release: release} }, "svc", "slow")

	req := e.Request(pid, ping{Val: 1}, 20*time.Millisecond)
	if _, err := req.Wait(); !errors.Is(err, ErrRequestTimeout) {
		t.Fatalf("err = %v, want timeout", err)
	}
	close(release) // 触发迟到的 Respond
	time.Sleep(30 * time.Millisecond)

	e.reqMu.Lock()
	n := len(e.requests)
	e.reqMu.Unlock()
	if n != 0 {
		t.Fatalf("requests not cleaned up: %d", n)
	}
}

// TestShutdownWakesBlockedSender：关停时唤醒阻塞在满邮箱上的发送方
func TestShutdownWakesBlockedSender(t *testing.T) {
	e := NewEngine(Config{MailboxSize: 1})
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	pid := e.Spawn(func() IActor {
		return &blockActor{entered: entered, release: release}
	}, "world", "room-1")

	e.Send(pid, "first")
	<-entered
	e.Send(pid, "second")

	sendDone := make(chan struct{})
	go func() {
		e.Send(pid, "blocked")
		close(sendDone)
	}()
	time.Sleep(30 * time.Millisecond) // 确保发送方已阻塞

	shutdownDone := make(chan struct{})
	go func() {
		e.Shutdown()
		close(shutdownDone)
	}()

	select {
	case <-sendDone:
	case <-time.After(1 * time.Second):
		t.Fatal("blocked sender not woken by Shutdown")
	}
	close(release) // 让 actor 退出 Receive，Shutdown 才能收尾
	select {
	case <-shutdownDone:
	case <-time.After(1 * time.Second):
		t.Fatal("Shutdown hung on blocked actor")
	}
}

// TestConcurrentSends：多 goroutine 并发 Send 到同一 actor，消息不丢
func TestConcurrentSends(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	a := &collectActor{}
	pid := e.Spawn(func() IActor { return a }, "world", "room-1")

	const workers, per = 8, 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				e.Send(pid, base+i)
			}
		}(w * per)
	}
	wg.Wait()
	a.waitCount(workers*per, t)

	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.msgs) != workers*per {
		t.Fatalf("messages lost: got %d, want %d", len(a.msgs), workers*per)
	}
}

type panicActor struct{}

func (a *panicActor) Receive(IActorContext) { panic("boom") }

// TestRestartEndToEnd：端到端验证崩溃重启——PID 不变，后续消息交给新实例
func TestRestartEndToEnd(t *testing.T) {
	e := NewEngine(Config{MaxRestarts: 3})
	defer e.Shutdown()
	first := true
	a := &collectActor{}
	pid := e.Spawn(func() IActor {
		if first {
			first = false
			return &panicActor{}
		}
		return a
	}, "world", "room-1")

	e.Send(pid, "boom")
	for i := 0; i < 3; i++ {
		e.Send(pid, i)
	}
	a.waitCount(3, t)
}

// TestGetPidsSorted：GetPids 按 ID 排序（确定性）
func TestGetPidsSorted(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	e.Spawn(testProducer(), "room", "b")
	e.Spawn(testProducer(), "room", "a")
	e.Spawn(testProducer(), "room", "c")
	pids := e.GetPids("room")
	want := []string{"room/a", "room/b", "room/c"}
	if len(pids) != 3 {
		t.Fatalf("pids = %v", pids)
	}
	for i := range want {
		if pids[i].ID != want[i] {
			t.Fatalf("pids = %v, want %v", pids, want)
		}
	}
}

// respondActor 收到普通消息也调用 Respond（无请求）→ 只记日志忽略，不 panic
type respondActor struct {
	mu    sync.Mutex
	count int
}

func (a *respondActor) Receive(ctx IActorContext) {
	ctx.Respond("nope")
	a.mu.Lock()
	a.count++
	a.mu.Unlock()
}

func (a *respondActor) waitCount(n int, t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		a.mu.Lock()
		got := a.count
		a.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("responded %d, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRespondWithoutRequest(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	a := &respondActor{}
	pid := e.Spawn(func() IActor { return a }, "svc", "r")
	e.Send(pid, "hi")
	a.waitCount(1, t)
}
