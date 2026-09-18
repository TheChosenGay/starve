package systems

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// bossWindupExecutor 把"Boss 正在起手某个技能"表达成一个带 windup 的权威动作。
//
// # 它为什么存在
//
// Boss 的技能效果由决策层节点在自己的前摇结束那一刻产生（SlamAOE / LeapTo /
// ThrowBomb，见 behavior/boss.go），但**客户端只靠副作用看不到"它正在放大招"**：
// 要等爆炸或位移发生，动画只能事后播。这个执行器让技能从一开始就有一个
// 逐 tick 复制的 ActionState，客户端据此播技能动画，并且：
//
//   - slam / roar：动作时长 == 节点的前摇 tick ⇒ 动作结束的那一刻正好是
//     效果发生的那一刻（客户端可以拿"动作结束"对齐特效）；
//   - throw / leap：效果是瞬时的，动作时长只是给客户端一个可播的动画长度。
//
// # 它刻意**没有**游戏效果
//
// Commit 返回零值（不改血量、不生成实体）。效果留在决策层的对应 tick：
// 这样"技能何时生效"只有一处真相，不会出现"动作 Commit 一次 + 节点又放一次"
// 的双重结算。等哪天把效果搬进 Commit（更纯粹的 P1.2 形态），把这里删掉即可。
type bossWindupExecutor struct{}

// Timing 用意图里的 Duration（决策节点自己的前摇 tick）作为 windup，recovery 固定 0。
//
// 为什么 recovery = 0：后摇仍由决策层节点计时（它把 Success 推迟到后摇结束），
// 动作再占一段 recovery 会让"动作还在跑"和"节点认为已经空了"两套时间互相打架，
// 表现为 Boss 该出拳时被动作忙挡住（丢连招）。
func (bossWindupExecutor) Timing(duration int64) (ActionTiming, bool) {
	if duration <= 0 {
		duration = 1
	}
	return ActionTiming{Windup: duration, Recovery: 0}, true
}

func (bossWindupExecutor) Validate(w *ecs.World, actor, _ ecs.Entity) ControlRejectReason {
	if !w.IsAlive(actor) || ecs.Has[components.Dead](w, actor) {
		return ControlRejectedInvalidActor
	}
	return ControlRejectedNone
}

func (bossWindupExecutor) Commit(*ecs.World, ecs.Entity, components.ActionState) ActionCommitResult {
	return ActionCommitResult{}
}
