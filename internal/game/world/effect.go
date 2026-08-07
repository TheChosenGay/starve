package world

import "starve/internal/actor"

// Effect 是副作用声明：命令/系统只 Emit，不执行；
// tick 结束时由 WorldActor.flushOutbox 统一投递（顺序确定、可断言）。
// isEffect 是未导出的"封印方法"：外部类型无法实现 Effect，
// 只能使用本包定义的三种效果（编译期保证）。
type Effect interface{ isEffect() }

// PushEffect 把快照/事件推送给客户端。
// To 为目标连接 ID（网关会话表），空串表示广播给所有在线会话；
// Route 为 pomelo route（见 pkg/proto），Payload 为 proto.Message（网关负责编码）。
type PushEffect struct {
	To      string
	Route   string
	Payload any
}

// SendMessageEffect 向另一个 actor 发消息（跨世界/服务调用）。
type SendMessageEffect struct {
	To  *actor.PID
	Msg any
}

// SaveEffect 事件触发的存档请求（如"每天开始"）：flushOutbox 时经
// SetSaveHandler 注入的出口落盘。手动保存走 SaveRequest，关服保存走 cmd/gate。
// 具体事件触发点（day_start 等）预留待实现。
type SaveEffect struct {
	Reason string
}

func (PushEffect) isEffect()        {}
func (SendMessageEffect) isEffect() {}
func (SaveEffect) isEffect()        {}
