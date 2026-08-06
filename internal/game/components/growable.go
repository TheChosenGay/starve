package components

// Growable 可生长（树等）：每 N tick 长一阶段。
type Growable struct {
	Stage int
	Ticks int // 距上次生长经过的 tick 数
}
