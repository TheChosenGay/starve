package world

import "time"

// Start 启动世界：WorldActor 收到后开始自驱动 tick（SendRepeat 给自己发 Tick）。
type Start struct{}

// Tick 是一个固定步长的模拟节拍（普通消息，与玩家命令在同一个邮箱里排队）。
type Tick struct{}

// QueryWorldTime 查询当前世界时钟（请求-应答，供外部/网关/测试使用）。
type QueryWorldTime struct{}

// WorldConfig 世界配置。
type WorldConfig struct {
	// TickInterval 模拟步长。默认 100ms（10Hz），生存玩法够用；
	// 动作手感要求高可降到 50ms（20Hz）。ECS tick 开销微秒级，余量充足。
	TickInterval time.Duration
	// BroadcastPositions 每 tick 把 Position 实体广播为 MovePush（M4 闭环用）。
	BroadcastPositions bool
}
