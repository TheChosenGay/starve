package world

// Start 启动世界：WorldActor 收到后开始自驱动 tick（SendRepeat 给自己发 Tick）。
type Start struct{}

// Tick 是一个固定步长的模拟节拍（普通消息，与玩家命令在同一个邮箱里排队）。
type Tick struct{}

// QueryWorldTime 查询当前世界时钟（请求-应答，供外部/网关/测试使用）。
type QueryWorldTime struct{}
