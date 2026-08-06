package components

// DayCycle 是世界级状态（Resource，非实体组件）：昼夜阶段 + 光照。
// 由 DayNightSystem 每 tick 推进，随快照推给客户端。
type DayCycle struct {
	Phase int
	Light float32 // 0..1
}
