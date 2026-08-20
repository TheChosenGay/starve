package actor

import (
	"errors"
	"testing"
	"time"
)

func TestMailboxOrdering(t *testing.T) {
	m := newMailbox(4)
	for i := 0; i < 3; i++ {
		if err := m.push(envelope{msg: i}); err != nil {
			t.Fatal(err)
		}
	}
	batch := m.popBatch(10)
	if len(batch) != 3 {
		t.Fatalf("batch len = %d", len(batch))
	}
	for i := range batch {
		if batch[i].msg != i {
			t.Fatalf("order broken at %d: %v", i, batch[i].msg)
		}
	}
}

func TestMailboxBatchLimit(t *testing.T) {
	m := newMailbox(8)
	for i := 0; i < 5; i++ {
		if err := m.push(envelope{msg: i}); err != nil {
			t.Fatal(err)
		}
	}
	batch := m.popBatch(2)
	if len(batch) != 2 {
		t.Fatalf("batch len = %d", len(batch))
	}
	rest := m.popBatch(10)
	if len(rest) != 3 {
		t.Fatalf("rest len = %d", len(rest))
	}
}

func TestMailboxBlockOnFull(t *testing.T) {
	m := newMailbox(1)
	if err := m.push(envelope{msg: 1}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- m.push(envelope{msg: 2})
	}()
	select {
	case err := <-done:
		t.Fatalf("push returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	batch := m.popBatch(1)
	if len(batch) != 1 {
		t.Fatal("pop empty")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("push still blocked after pop")
	}
}

func TestMailboxCloseWakesBlockedPush(t *testing.T) {
	m := newMailbox(1)
	if err := m.push(envelope{msg: 1}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- m.push(envelope{msg: 2})
	}()
	time.Sleep(30 * time.Millisecond)
	m.close()
	select {
	case err := <-done:
		if !errors.Is(err, ErrMailboxClosed) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("blocked push not woken by close")
	}
}

func TestMailboxCloseWakesBlockedPop(t *testing.T) {
	m := newMailbox(2)
	done := make(chan []envelope, 1)
	go func() {
		done <- m.popBatch(10)
	}()
	time.Sleep(30 * time.Millisecond)
	m.close()
	select {
	case batch := <-done:
		if batch != nil {
			t.Fatalf("batch = %v, want nil", batch)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("blocked pop not woken by close")
	}
}

func TestMailboxPushTimeout(t *testing.T) {
	m := newMailbox(1)
	if err := m.push(envelope{msg: 1}); err != nil {
		t.Fatal(err)
	}
	// 已满：限时入队应超时
	if err := m.pushTimeout(envelope{msg: 2}, 50*time.Millisecond); !errors.Is(err, ErrMailboxTimeout) {
		t.Fatalf("err = %v", err)
	}
	// 弹出后：限时入队应成功
	if batch := m.popBatch(10); len(batch) != 1 {
		t.Fatalf("batch = %v", batch)
	}
	if err := m.pushTimeout(envelope{msg: 3}, time.Second); err != nil {
		t.Fatalf("pushTimeout after space: %v", err)
	}
}

func TestMailboxSnapshotIsReadOnlyApproximation(t *testing.T) {
	m := newMailbox(3)
	if err := m.push(envelope{msg: "queued"}); err != nil {
		t.Fatal(err)
	}
	snapshot := m.snapshot("world")
	if snapshot.Kind != "world" || snapshot.Depth != 1 || snapshot.Capacity != 3 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	snapshot.Depth = 99
	if got := m.snapshot("world").Depth; got != 1 {
		t.Fatalf("mutating snapshot changed mailbox depth to %d", got)
	}
}

func TestEngineMailboxSnapshotsExcludeClosedActors(t *testing.T) {
	engine := NewEngine(Config{})
	defer engine.Shutdown()
	pid := engine.Spawn(func() IActor { return &collectActor{} }, "world", "closed")

	if _, ok := engine.MailboxSnapshot(pid); !ok {
		t.Fatal("live actor mailbox missing")
	}
	engine.Poison(pid)

	deadline := time.Now().Add(time.Second)
	for {
		if _, ok := engine.MailboxSnapshot(pid); !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closed actor remained in mailbox snapshots")
		}
		time.Sleep(time.Millisecond)
	}
	for _, snapshot := range engine.MailboxSnapshots() {
		if snapshot.Kind == "world" {
			t.Fatalf("closed actor leaked into aggregate snapshots: %+v", snapshot)
		}
	}
}
