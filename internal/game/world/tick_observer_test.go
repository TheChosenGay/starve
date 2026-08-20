package world

import (
	"reflect"
	"testing"
	"time"

	"starve/internal/actor"
)

func TestWorldActorReportsTickStats(t *testing.T) {
	engine := actor.NewEngine(actor.Config{})
	defer engine.Shutdown()

	observed := make(chan TickStats, 1)
	wa := NewWorldActor(WorldConfig{TickInterval: 50 * time.Millisecond})
	wa.SetTickObserver(TickObserverFunc(func(stats TickStats) {
		observed <- stats
	}))
	pid := engine.Spawn(func() actor.IActor { return wa }, "world", "observer-test")

	engine.Send(pid, Command{})
	engine.Send(pid, Tick{})

	select {
	case stats := <-observed:
		if stats.Commands != 1 {
			t.Fatalf("commands = %d, want 1", stats.Commands)
		}
		if stats.Duration <= 0 {
			t.Fatalf("duration = %v, want > 0", stats.Duration)
		}
		if stats.Effects == 0 {
			t.Fatal("tick should report snapshot effect")
		}
		if stats.DeltaSnapshotBytes <= 0 {
			t.Fatal("tick should report encoded delta snapshot bytes")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for tick observation")
	}
}

func TestCompositeTickObserverSkipsNilAndFansOut(t *testing.T) {
	var first, second TickStats
	stats := TickStats{Tick: 42, DeltaSnapshotBytes: 128}
	CompositeTickObserver{
		TickObserverFunc(func(got TickStats) { first = got }),
		nil,
		TickObserverFunc(func(got TickStats) { second = got }),
	}.ObserveTick(stats)
	if !reflect.DeepEqual(first, stats) || !reflect.DeepEqual(second, stats) {
		t.Fatalf("observers got %+v and %+v, want %+v", first, second, stats)
	}
}
