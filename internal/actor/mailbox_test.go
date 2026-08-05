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
