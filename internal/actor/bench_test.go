package actor

import (
	"strconv"
	"testing"
	"time"
)

// 运行：go test -run '^$' -bench . -benchmem ./internal/actor/

type benchBarrier struct{}

// barrierActor 收到 benchBarrier 后关 done，作为"排干"信号：
// 基准里先发 N 条消息再发 barrier，收到 barrier 说明 N 条已全部处理完。
type barrierActor struct {
	done chan struct{}
}

func (a *barrierActor) Receive(ctx IActorContext) {
	switch ctx.Message().(type) {
	case benchBarrier:
		close(a.done)
	}
}

// BenchmarkMailboxPushPop：邮箱原始吞吐（push + pop，不含 actor 开销）
func BenchmarkMailboxPushPop(b *testing.B) {
	m := newMailbox(1024)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := m.push(envelope{msg: i}); err != nil {
			b.Fatal(err)
		}
		if batch := m.popBatch(1); len(batch) != 1 {
			b.Fatal("pop empty")
		}
	}
}

// BenchmarkEngineSend：单发送者 → 单 actor（发送 + 处理端到端吞吐）
func BenchmarkEngineSend(b *testing.B) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	done := make(chan struct{})
	pid := e.Spawn(func() IActor { return &barrierActor{done: done} }, "bench", "worker")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Send(pid, i)
	}
	e.Send(pid, benchBarrier{})
	<-done // 排干：等所有消息处理完
	b.StopTimer()
}

// BenchmarkEngineSendConcurrent：8 路并发发送 → 单 actor
func BenchmarkEngineSendConcurrent(b *testing.B) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	done := make(chan struct{})
	pid := e.Spawn(func() IActor { return &barrierActor{done: done} }, "bench", "worker")

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			e.Send(pid, 1)
		}
	})
	e.Send(pid, benchBarrier{})
	<-done
	b.StopTimer()
}

// forwardActor 把收到的消息转发给 target（两跳链路的中间节点）
type forwardActor struct {
	target *PID
}

func (a *forwardActor) Receive(ctx IActorContext) {
	switch m := ctx.Message().(type) {
	case int:
		ctx.Send(a.target, m)
	case benchBarrier:
		ctx.Send(a.target, m)
	}
}

// BenchmarkActorToActor：actor → actor 两跳（fwd → sink）吞吐
func BenchmarkActorToActor(b *testing.B) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	done := make(chan struct{})
	sinkPID := e.Spawn(func() IActor { return &barrierActor{done: done} }, "bench", "sink")
	fwdPID := e.Spawn(func() IActor { return &forwardActor{target: sinkPID} }, "bench", "fwd")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Send(fwdPID, i)
	}
	e.Send(fwdPID, benchBarrier{})
	<-done
	b.StopTimer()
}

type benchPing struct{ Val int }
type benchPong struct{ Val int }

type echoBenchActor struct{}

func (a *echoBenchActor) Receive(ctx IActorContext) {
	switch m := ctx.Message().(type) {
	case benchPing:
		ctx.Respond(benchPong{Val: m.Val})
	}
}

// BenchmarkRequestResponse：请求-应答往返（串行延迟）
func BenchmarkRequestResponse(b *testing.B) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	pid := e.Spawn(func() IActor { return &echoBenchActor{} }, "bench", "echo")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := e.Request(pid, benchPing{Val: i}, time.Second)
		if _, err := req.Wait(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
}

// BenchmarkSpawn：Spawn 一个 actor 的开销（不启动 goroutine）
func BenchmarkSpawn(b *testing.B) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		e.Spawn(func() IActor { return &barrierActor{done: make(chan struct{})} }, "bench", strconv.Itoa(i))
	}
	b.StopTimer()
}
