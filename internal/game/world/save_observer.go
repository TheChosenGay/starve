package world

import "time"

// SaveTrigger 是有界的存档来源标签。
type SaveTrigger string

const (
	SaveTriggerManual   SaveTrigger = "manual"
	SaveTriggerEvent    SaveTrigger = "event"
	SaveTriggerShutdown SaveTrigger = "shutdown"
)

// SaveStats 是一次存档尝试的不可变观测数据。
type SaveStats struct {
	Duration time.Duration
	Bytes    int
	Trigger  SaveTrigger
	Err      error
}

// SaveObserver 隔离存档业务与具体观测 SDK。
type SaveObserver interface {
	ObserveSave(SaveStats)
}

type SaveObserverFunc func(SaveStats)

func (f SaveObserverFunc) ObserveSave(stats SaveStats) { f(stats) }
