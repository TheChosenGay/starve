package world

import "github.com/TheChosenGay/actor"

// Effect 是副作用声明：命令/系统只 Emit，不执行；
// tick 结束时由 WorldActor.flushOutbox 统一投递（顺序确定、可断言）。
// isEffect 是未导出的"封印方法"：外部类型无法实现 Effect，
// 只能使用本包定义的三种效果（编译期保证）。
type Effect interface{ isEffect() }

// PushEffect 把快照/事件推送给客户端。
// UID 优先：按玩家会话投递（视野增量）。
// To 为目标连接 ID；UID 与 To 都空则广播给所有在线会话。
// Route 为 pomelo route（见 pkg/proto），Payload 为 proto.Message（网关负责编码）。
type PushEffect struct {
	To        string
	UID       string
	Route     string
	Payload   any
	WorldTick uint64              // 快照类推送的世界 tick；0 表示未设置
	InputAcks map[string]InputAck // UID → 当前输入世代与 ACK；仅快照推送使用
}

// InputAck 是玩家输入流的不可变确认快照。
type InputAck struct {
	Epoch uint64
	Seq   uint64
}

// SendMessageEffect 向另一个 actor 发消息（跨世界/服务调用）。
type SendMessageEffect struct {
	To  *actor.PID
	Msg any
}

func (PushEffect) isEffect()        {}
func (SendMessageEffect) isEffect() {}
