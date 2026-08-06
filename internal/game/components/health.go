package components

// Health 生命值（纯数据，无锁）。
type Health struct {
	Cur int
	Max int
}
