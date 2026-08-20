package world

// Start 启动世界：WorldActor 收到后开始自驱动 tick（SendRepeat 给自己发 Tick）。
type Start struct{}

// Tick 是一个固定步长的模拟节拍（普通消息，与玩家命令在同一个邮箱里排队）。
type Tick struct{}

// BeginInputEpoch 切换某玩家当前有效的输入世代并重置 ACK。
// Gateway 在登录成功后、回复客户端前发送；旧世代迟到命令会被 WorldActor 丢弃。
type BeginInputEpoch struct {
	UID   string
	Epoch uint64
}

// QueryWorldTime 查询当前世界时钟（请求-应答，供外部/网关/测试使用）。
type QueryWorldTime struct{}
