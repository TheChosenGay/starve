package actor

import (
	"sync"
	"testing"
	"time"
)

// ---- 消息类型 ----
type ping struct{ Val int }
type pong struct{ Val int }
type whoAmI struct{}
type startPing struct{}
type spawnChildMsg struct{ name string }

// ---- 测试 actor ----
type echoActor struct {
	lastSender *PID
}

func (a *echoActor) Receive(ctx IActorContext) {
	switch m := ctx.Message().(type) {
	case ping:
		a.lastSender = ctx.Sender()
		ctx.Respond(pong{Val: m.Val * 2})
	case whoAmI:
		ctx.Respond(a.lastSender)
	}
}

type collectActor struct {
	mu   sync.Mutex
	msgs []any
}

func (a *collectActor) Receive(ctx IActorContext) {
	a.mu.Lock()
	a.msgs = append(a.msgs, ctx.Message())
	a.mu.Unlock()
}

func (a *collectActor) waitCount(n int, t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		a.mu.Lock()
		got := len(a.msgs)
		a.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("received %d, want %d", got, n)
		}
		time.Sleep(time.Millisecond)
	}
}

type flakyActor struct {
	msgs    []any
	panicOn int // 第几条消息 panic（每条实例从 1 数）
}

func (a *flakyActor) Receive(ctx IActorContext) {
	if len(a.msgs)+1 == a.panicOn {
		panic("boom")
	}
	a.msgs = append(a.msgs, ctx.Message())
}

type parentActor struct{}

func (a *parentActor) Receive(ctx IActorContext) {
	switch m := ctx.Message().(type) {
	case spawnChildMsg:
		ctx.SpawnChild(func() IActor { return &noopActor{} }, m.name)
	}
}

// ---- 测试 ----

func TestActorDelivery(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	a := &collectActor{}
	pid := e.Spawn(func() IActor { return a }, "world", "room-1")
	for i := 0; i < 5; i++ {
		e.Send(pid, i)
	}
	a.waitCount(5, t)
}

func TestActorDeliveryAndRespond(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	pid := e.Spawn(func() IActor { return &echoActor{} }, "svc", "echo")

	resp := e.Request(pid, ping{Val: 21}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if v != (pong{Val: 42}) {
		t.Fatalf("v = %#v", v)
	}
}

func TestContextRequestSenderPropagation(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	pingerPID := e.Spawn(func() IActor { return &pinger{} }, "pinger", "p1")
	echoPID = e.Spawn(func() IActor { return &echoActor{} }, "svc", "echo")

	// pinger 收到 startPing 后 ctx.Request(echo)，echo 回复后 pinger 再 Respond 给外部
	resp := e.Request(pingerPID, startPing{}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if v != (pong{Val: 10}) {
		t.Fatalf("v = %#v", v)
	}

	// 问 echo：上一轮 Request 的 sender 是不是 pinger
	resp2 := e.Request(echoPID, whoAmI{}, time.Second)
	v2, err := resp2.Wait()
	if err != nil {
		t.Fatal(err)
	}
	pid, ok := v2.(*PID)
	if !ok || pid.ID != pingerPID.ID {
		t.Fatalf("sender = %v, want %v", v2, pingerPID)
	}
}

type pinger struct{}

func (a *pinger) Receive(ctx IActorContext) {
	switch ctx.Message().(type) {
	case startPing:
		resp := ctx.Request(echoPID, ping{Val: 5}, time.Second)
		v, err := resp.Wait()
		if err != nil {
			ctx.Respond(err)
			return
		}
		ctx.Respond(v)
	}
}

var echoPID *PID

func TestPanicRestartPreservesBatch(t *testing.T) {
	e := NewEngine(Config{MaxRestarts: 2})
	// 第一个实例第一条消息 panic；重启后的新实例正常处理剩余消息
	first := true
	producer := func() IActor {
		if first {
			first = false
			return &flakyActor{panicOn: 1}
		}
		return &flakyActor{}
	}
	pid := e.Spawn(producer, "world", "room-1")
	p := e.lookup(pid)

	// 直接喂一批消息（模拟已从邮箱取出）：第一条 panic → 重启 → 剩余 2 条交付给新实例
	ok := p.deliverBatch(e, []envelope{{msg: "bad"}, {msg: 1}, {msg: 2}})
	if !ok {
		t.Fatal("should survive restart")
	}
	a := p.actor.(*flakyActor)
	if len(a.msgs) != 2 || a.msgs[0] != 1 || a.msgs[1] != 2 {
		t.Fatalf("delivered = %v", a.msgs)
	}
}

func TestMaxRestartsStopsActor(t *testing.T) {
	e := NewEngine(Config{MaxRestarts: 1})
	// 每个实例第一条消息都 panic → 超过 MaxRestarts 后永久停止
	pid := e.Spawn(func() IActor { return &flakyActor{panicOn: 1} }, "world", "room-1")
	p := e.lookup(pid)

	ok := p.deliverBatch(e, []envelope{{msg: "a"}, {msg: "b"}, {msg: "c"}})
	if ok {
		t.Fatal("should stop after max restarts")
	}
	if !p.dead {
		t.Fatal("process not marked dead")
	}
	e.Send(pid, "x") // 邮箱已关闭：dead letter，不 panic 不阻塞
}

func TestSpawnChild(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	parentPID := e.Spawn(func() IActor { return &parentActor{} }, "world", "room-1")
	e.Send(parentPID, spawnChildMsg{name: "camera"})

	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := e.GetPid("world", "room-1.camera"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child not spawned")
		}
		time.Sleep(time.Millisecond)
	}
	// 父 actor 的监督列表里应有一条子 actor
	p := e.lookup(parentPID)
	if len(p.children) != 1 {
		t.Fatalf("children = %d, want 1", len(p.children))
	}
}
