package world

import (
	"context"
	"log/slog"
	"time"
)

// TickStats is an immutable observation emitted after one world tick.
// It deliberately contains no ECS or actor implementation types.
type TickStats struct {
	WorldID            string
	Tick               int64
	Duration           time.Duration
	Commands           int
	DirtyEntities      int
	RemovedEntities    int
	Effects            int
	DeltaSnapshotBytes int
	ActiveActions      int
	ActionEvents       []ActionStat
	ImpactEvents       []ImpactStat
	HealthEvents       []HealthChangeStat
}

// ActionStat 是不含实体/请求 ID 的低基数动作观测事件。
type ActionStat struct {
	Stage  string
	Kind   string
	Reason string
}

type ImpactStat struct {
	Result string
}

type HealthChangeStat struct {
	Cause string
}

// TickObserver receives operational data outside deterministic simulation.
// Implementations must not mutate the world or block the actor.
type TickObserver interface {
	ObserveTick(TickStats)
}

// TickObserverFunc adapts a function to TickObserver, primarily for tests.
type TickObserverFunc func(TickStats)

func (f TickObserverFunc) ObserveTick(stats TickStats) { f(stats) }

// CompositeTickObserver 把同一份不可变统计扇出到多个独立适配器。
type CompositeTickObserver []TickObserver

func (o CompositeTickObserver) ObserveTick(stats TickStats) {
	for _, observer := range o {
		if observer != nil {
			observer.ObserveTick(stats)
		}
	}
}

// SlogTickObserver emits structured slow-tick warnings and sampled debug logs.
type SlogTickObserver struct {
	logger      *slog.Logger
	worldID     string
	budget      time.Duration
	sampleEvery int64
}

func NewSlogTickObserver(
	logger *slog.Logger,
	worldID string,
	budget time.Duration,
	sampleEvery int64,
) *SlogTickObserver {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogTickObserver{
		logger:      logger,
		worldID:     worldID,
		budget:      budget,
		sampleEvery: sampleEvery,
	}
}

func (o *SlogTickObserver) ObserveTick(stats TickStats) {
	stats.WorldID = o.worldID
	level := slog.LevelDebug
	message := "world tick"
	if o.budget > 0 && stats.Duration > o.budget {
		level = slog.LevelWarn
		message = "world tick over budget"
	} else if o.sampleEvery <= 0 || stats.Tick%o.sampleEvery != 0 {
		return
	}
	o.logger.Log(
		context.Background(),
		level,
		message,
		"world_id", stats.WorldID,
		"tick", stats.Tick,
		"duration_ms", float64(stats.Duration.Microseconds())/1000,
		"commands", stats.Commands,
		"dirty_entities", stats.DirtyEntities,
		"removed_entities", stats.RemovedEntities,
		"effects", stats.Effects,
		"delta_snapshot_bytes", stats.DeltaSnapshotBytes,
		"active_actions", stats.ActiveActions,
	)
}
