package actor

import (
	"errors"
	"testing"
	"time"
)

func testProducer() Producer {
	return func() IActor { return &noopActor{} }
}

type noopActor struct{}

func (a *noopActor) Receive(msg any) {}

func TestSpawnAndLookup(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()

	pid := e.Spawn(testProducer(), "world", "room-1")
	if pid.ID != "world/room-1" || pid.Address != LocalLookupAddr {
		t.Fatalf("pid = %+v", pid)
	}
	got, ok := e.GetPid("world", "room-1")
	if !ok || *got != *pid {
		t.Fatalf("GetPid = %+v, %v", got, ok)
	}
	pids := e.GetPids("world")
	if len(pids) != 1 || *pids[0] != *pid {
		t.Fatalf("GetPids = %v", pids)
	}
	if got := e.GetPids("nope"); len(got) != 0 {
		t.Fatalf("GetPids(nope) = %v", got)
	}
}

func TestSpawnAutoName(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	a := e.Spawn(testProducer(), "agent", "")
	b := e.Spawn(testProducer(), "agent", "")
	if a.ID != "agent/1" || b.ID != "agent/2" {
		t.Fatalf("ids = %s, %s", a.ID, b.ID)
	}
}

func TestSpawnDuplicatePanics(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	e.Spawn(testProducer(), "world", "room-1")
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate spawn")
		}
	}()
	e.Spawn(testProducer(), "world", "room-1")
}

func TestSpawnNilProducerPanics(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on nil producer")
		}
	}()
	e.Spawn(nil, "world", "room-1")
}

func TestSendUnknownPIDDeadLetter(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	e.Send(&PID{Address: LocalLookupAddr, ID: "nope"}, "hi") // 不 panic
}

func TestSendRemotePIDDeadLetter(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	// M6 之前远程地址统一走 dead letter
	e.Send(&PID{Address: "node-2:8080", ID: "world/room-1"}, "hi")
}

func TestASendUnknownPID(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	err := e.ASend(&PID{Address: LocalLookupAddr, ID: "nope"}, "hi", time.Second)
	if !errors.Is(err, ErrDeadLetter) {
		t.Fatalf("err = %v", err)
	}
}

func TestASendDelivers(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	a := &collectActor{}
	pid := e.Spawn(func() IActor { return a }, "world", "room-1")
	if err := e.ASend(pid, "hi", time.Second); err != nil {
		t.Fatal(err)
	}
	a.waitCount(1, t)
}

func TestRequestTimeout(t *testing.T) {
	e := NewEngine(Config{})
	defer e.Shutdown()
	pid := e.Spawn(testProducer(), "world", "room-1")
	resp := e.Request(pid, "hi", 30*time.Millisecond)
	start := time.Now()
	_, err := resp.Wait()
	if !errors.Is(err, ErrRequestTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) < 25*time.Millisecond {
		t.Fatal("returned too early")
	}
}

func TestShutdownIdempotent(t *testing.T) {
	e := NewEngine(Config{})
	e.Spawn(testProducer(), "world", "room-1")
	e.Shutdown()
	e.Shutdown()
}

func TestSendAfterShutdown(t *testing.T) {
	e := NewEngine(Config{})
	pid := e.Spawn(testProducer(), "world", "room-1")
	e.Shutdown()
	e.Send(pid, "hi") // 不 panic、不阻塞
}
