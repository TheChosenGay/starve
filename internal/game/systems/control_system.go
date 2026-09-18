package systems

import (
	"sort"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// ControlIntentKind 是 ControlSystem 可仲裁的控制类别。
type ControlIntentKind uint8

const (
	ControlMove ControlIntentKind = iota + 1
	ControlStartAction
	ControlCancelAction
)

// ControlRejectReason 是有界业务拒绝原因。
type ControlRejectReason uint8

const (
	ControlRejectedNone ControlRejectReason = iota
	ControlRejectedInvalidActor
	ControlRejectedBusy
	ControlRejectedInvalidTarget
	ControlRejectedUnsupportedAction
)

// ControlIntent 是单 tick 瞬时控制意图。队列顺序就是仲裁顺序。
type ControlIntent struct {
	Kind        ControlIntentKind
	Actor       ecs.Entity
	ArrivalID   uint64
	Seq         uint64
	ActionKind  components.ActionKind
	Target      ecs.Entity
	RequestID   uint64
	Duration    int64
	RecipeID    string
	Ingredients []components.ItemStack
	DX, DY      int
	Path        []components.MoveDir
	// AimX/AimY 是投掷动作的目标落点（格）。
	//
	// 为什么放在 ControlIntent 而不是组件里：意图是"这一次请求"的全部输入，
	// 落点属于请求本身（客户端点哪就扔哪）。放组件会让并发意图互相覆盖。
	// 只有 ActionThrow 使用它，其他动作忽略。
	AimX, AimY float64
	HasAim     bool
}

// ControlResult 记录本 tick 的接纳结果，供测试、指标或上层适配器读取。
type ControlResult struct {
	Intent     ControlIntent
	Accepted   bool
	Superseded bool
	// Pending 表示这条移动意图已入队、但本 tick 还没被消费（后面的 tick 会按顺序消费）。
	Pending bool
	Reason  ControlRejectReason
}

// ControlQueue 是世界级 ECS Resource；Intents 每 tick 由 ControlSystem 消费。
type ControlQueue struct {
	Intents       []ControlIntent
	Results       []ControlResult
	nextArrivalID uint64
	nextActionID  uint64

	// Pending 是每个 Actor 还没消费的**客户端操作**（FIFO，按 seq 顺序；移动/攻击/取消共用一条队列）。
	//
	// 为什么必须共用一条队列：客户端所有操作共用同一个自增 seq，序号顺序就是玩家的操作顺序。
	// 分开排队（比如移动一个、攻击一个）会让 move(5) 被 attack(6) 插队 —— 顺序反了，
	// 客户端"第 N 条操作之后的状态"就和权威对不上。
	//
	// 为什么不"最后一条赢"：客户端每 tick 采样一条（长按的每 tick 重复、短按只跨 1~2 个 tick），
	// 而网络抖动必然把几条挤进同一个服务端 tick。只留最后一条 ⇒ 被丢的那几条在服务端**永远不生效**
	// ⇒ 和解必然出现假失配。排队 + 按序消费（积压时追步）才是"在服务端复现客户端操作流"。
	//
	// ⚠️ 队列长度上限见 ControlQueue.MaxPending——超出丢最旧并计数，防止时钟异常/坏客户端把它撑爆。
	Pending map[ecs.Entity][]pendingOp

	// Steps 是本 tick 消费掉的 Move 操作对应的**逐步方向**（按 seq 顺序）。
	//
	// 为什么需要它：追步要求"K 条 Move 配 K 步移动积分"，而每步必须用它自己那条的方向
	// （否则 K 步全用最后一条的方向，轨迹和客户端不一致）。MoveSystem 按这张表跑子步。
	Steps map[ecs.Entity][]components.MoveDir

	// Consumed 是本 tick 真正消费掉的操作（供 WorldActor 推进对外的 ACK）。
	// ACK 的语义是"这条已经烘进这份快照的自己状态"，所以必须在这里记，而不是在命令入队时记。
	Consumed []ConsumedOp

	// OpDriven 记录"由客户端操作流驱动"的 Actor（收到过带 seq 的操作）。
	//
	// 为什么必须记住它：MoveSystem 对"没有逐步方向表"的实体会按**保留意图继续走 round 0**
	// （effectiveDir = Path 队首或 mv.Dir）—— 那是 AI/生物的语义。对操作驱动的玩家这是**错的**：
	// 这一 tick 没消费到操作（包还在路上/被抖动挤到下一个 tick），服务端却拿旧意图白走一步，
	// 而 ACK 不涨 ⇒ "第 S 条操作之后的状态"在服务端不再是这个状态（实测恒定偏 0.5 格 = 1 个
	// tick 的位移，转向处翻倍到 1.0+），客户端于是每份快照都要校正一次、偶尔撞上"直接贴"阈值
	// —— 这正是"走得越久越卡"的根。
	//
	// 修法：这些 Actor 每 tick 都在 Steps 里占一个条目（没有操作时是**空条目**），
	// MoveSystem 看到"有表但轮次用完"就不会替它走（见 participatesInRound）。
	OpDriven map[ecs.Entity]struct{}

	// StepBudget 是**单个 Actor 单 tick 最多跑几步移动**（= 最多消费几条 Move 操作）。
	//
	// 为什么需要它：客户端每 tick 采样一条操作，但网络抖动会把几条挤进同一个服务端 tick。
	// 若一个 tick 只消费一条，队列会变成随机游走（进 1 出 1，突发后永远回不到 0），
	// 玩家的操作会被**永久**推后。允许追步（K>1）才有把队列拉回 0 的回复力。
	// 上限同时是防加速外挂的闸门：一帧最多 K×50ms 的移动。
	// 0 = 用缺省值（3）。
	StepBudget int

	// MaxPending 是单个 Actor 队列长度上限：超出丢**最旧**并计数（Dropped）。
	// 0 = 用缺省值（8）。宁可丢操作（客户端会从权威状态重新对齐），也不要让它无界增长。
	MaxPending int

	// Dropped 累计丢弃的操作条数（指标出口）。
	Dropped uint64

	// consumed / gapWait 是"**按序号连续消费**"的记账：
	//   · 只有队首正好是 consumed+1 才消费 —— 否则说明中间那条还没到（乱序/丢包），
	//     等它补齐（客户端每 tick 冗余上传未确认窗口会把缺口填上）；
	//   · 等超过 maxGapWaitTicks 还不来，才跳过并计一次 Desync（客户端会因为权威不同而校正回来）。
	// 为什么必须这样：跨过缺口消费会让两边的操作流永久错位（实测 300ms+抖动下 49/125 份快照
	// 的"同序号状态"对不上、误差 0.5~3.5 格）—— 客户端以为第 N 条之后是这个状态，服务端却少走了几条。
	consumed map[ecs.Entity]uint64
	gapWait  map[ecs.Entity]int

	// Desyncs 累计"等不到缺口、只能跳过"的次数（指标出口；>0 说明链路丢包超出冗余窗口）。
	Desyncs uint64

	// CatchupExtra 本 tick 因追步**多跑**的移动步数（= 各 Actor 步数减 1 之和，观测出口）。
	CatchupExtra int
}

// pendingOp 是排队中的一条**客户端操作**（移动/攻击/取消都进这一条队列，按 seq 保序）。
//
// ResultIndex 只在"这条是**本 tick** 入队"时有效；跨 tick 消费时 Results 已经重建，
// 回填前要用 ArrivalID 核对，避免写错人。
type pendingOp struct {
	Intent      ControlIntent
	ResultIndex int
}

// ConsumedOp 是一条"已被消费"的操作（移动/动作/取消都可能）。
//
// 配对 ACK 用：WorldActor 拿它推进对外的"已完整应用完到第几条"，
// 并保留"应用完这条之后的状态"给客户端做同序号比对。
type ConsumedOp struct {
	Entity   ecs.Entity
	Seq      uint64
	Kind     ControlIntentKind
	Accepted bool
	Reason   ControlRejectReason
}

// StepBudgetDefault / MaxPendingDefault：缺省预算。
//
// StepBudgetDefault 是单个 Actor 单 tick 最多跑的**移动步数**（= 最多消费几条 Move 操作）。
//
// 为什么要有：客户端每 tick 采样一条操作，网络抖动会把几条挤进同一个服务端 tick。
// 若一个 tick 只消费一条，队列就变成随机游走（进 1 出 1，突发后永远回不到 0），
// 玩家的操作会被**永久**推后。允许追步（K>1）才有把队列拉回 0 的回复力。
// 上限同时是防加速外挂的闸门：一帧最多 K×50ms 的移动（3 步 = 150ms）。
//
// ⚠️ 步数由 MoveSystem 逐个"子步"跑完（K 条 Move 配 K 步，每步用各自那条的方向），
// 绝不出现"消费了没走"——否则客户端"第 N 条操作之后的状态"在两边就不是同一个东西了。
const (
	StepBudgetDefault = 3
	MaxPendingDefault = 8

	// maxGapWaitTicks 是"缺口最多等几个 tick"（等不到就跳过并计 Desync）。
	//
	// 为什么是 3 而不是更大：
	//   · 我们的传输是 **WebSocket(TCP)** —— 连接内**有序、不丢**，缺口只可能来自
	//     断线重连（断开期间的操作没发出去）或换代/客户端异常，属于"补不回来"的情况；
	//   · 主流（UDP 的 Quake/Source 那一脉）也不会一直等：它们靠**每包冗余携带未确认命令**
	//     让丢包在一个包间隔内被补齐，超过就**跳过**——因为等待的代价是"缺口之后的输入全被卡住"，
	//     比丢一条操作更伤手感；
	//   · 3 tick（150ms）足够让"重连后补发未确认窗口"赶上，又不至于把后续输入压太久。
	//
	// 跳过之后两边会差"那条操作的位移"，客户端下一次和解会 rebase + 按自己的节拍重放 ⇒ 自愈。
	maxGapWaitTicks = 3
)

// stepBudget 本 tick 每个 Actor 的移动步数预算（0/负数 = 用缺省）。
func (q *ControlQueue) stepBudget() int {
	if q.StepBudget < 1 {
		return StepBudgetDefault
	}
	return q.StepBudget
}

// maxPending 单个 Actor 的队列上限（0 = 缺省）。
func (q *ControlQueue) maxPending() int {
	if q.MaxPending < 1 {
		return MaxPendingDefault
	}
	return q.MaxPending
}

func EnqueueControl(w *ecs.World, intent ControlIntent) {
	q := ecs.Resource[ControlQueue](w)
	q.nextArrivalID++
	intent.ArrivalID = q.nextArrivalID
	if len(intent.Path) > 0 {
		intent.Path = append([]components.MoveDir(nil), intent.Path...)
	}
	if len(intent.Ingredients) > 0 {
		intent.Ingredients = append([]components.ItemStack(nil), intent.Ingredients...)
	}
	q.Intents = append(q.Intents, intent)
}

func MoveIntent(actor ecs.Entity, dx, dy int, seq uint64) ControlIntent {
	return ControlIntent{Kind: ControlMove, Actor: actor, Seq: seq, DX: dx, DY: dy}
}

func PathIntent(actor ecs.Entity, path []components.MoveDir, seq uint64) ControlIntent {
	return ControlIntent{Kind: ControlMove, Actor: actor, Seq: seq, Path: path}
}

func StartActionIntent(
	actor ecs.Entity,
	kind components.ActionKind,
	target ecs.Entity,
	seq, requestID uint64,
) ControlIntent {
	return ControlIntent{
		Kind:       ControlStartAction,
		Actor:      actor,
		Seq:        seq,
		ActionKind: kind,
		Target:     target,
		RequestID:  requestID,
	}
}

// ThrowIntent 构造投掷意图（带目标落点）。
//
// 单独一个构造函数而不是给 StartActionIntent 加参数：投掷是唯一需要
// 坐标落点的动作，给通用构造函数加参数会让其他 6 个调用点都要改。
func ThrowIntent(
	actor, thrown ecs.Entity,
	aimX, aimY float64,
	seq, requestID uint64,
) ControlIntent {
	return ControlIntent{
		Kind:       ControlStartAction,
		Actor:      actor,
		Seq:        seq,
		ActionKind: components.ActionThrow,
		Target:     thrown,
		RequestID:  requestID,
		AimX:       aimX,
		AimY:       aimY,
		HasAim:     true,
	}
}

func StartCraftIntent(
	actor ecs.Entity,
	recipeID string,
	durationTicks int,
	ingredients []components.ItemStack,
	seq, requestID uint64,
) ControlIntent {
	return ControlIntent{
		Kind:        ControlStartAction,
		Actor:       actor,
		Seq:         seq,
		ActionKind:  components.ActionCraft,
		RequestID:   requestID,
		Duration:    int64(durationTicks),
		RecipeID:    recipeID,
		Ingredients: ingredients,
	}
}

// HasPendingAction 判断实体是否已有尚未仲裁的动作开始意图。
func HasPendingAction(w *ecs.World, actor ecs.Entity) bool {
	for _, intent := range ecs.Resource[ControlQueue](w).Intents {
		if intent.Actor == actor && intent.Kind == ControlStartAction {
			return true
		}
	}
	return false
}

// ControlSystem 对每个 Actor 严格执行本 tick 最后到达的控制意图。
type ControlSystem struct{}

func (s *ControlSystem) Update(w *ecs.World, dt time.Duration) {
	q := ecs.Resource[ControlQueue](w)
	q.Results = make([]ControlResult, len(q.Intents))
	q.Consumed = q.Consumed[:0]
	q.CatchupExtra = 0
	if q.consumed == nil {
		q.consumed = make(map[ecs.Entity]uint64)
		q.gapWait = make(map[ecs.Entity]int)
	}
	for k := range q.Steps {
		delete(q.Steps, k)
	}
	consumedThisTick := make(map[ecs.Entity]bool, len(q.Intents))
	winner := make(map[ecs.Entity]int, len(q.Intents))
	actorOrder := make([]ecs.Entity, 0, len(q.Intents))
	seen := make(map[ecs.Entity]bool, len(q.Intents))
	for i, intent := range q.Intents {
		q.Results[i].Intent = intent
		if !seen[intent.Actor] {
			seen[intent.Actor] = true
			actorOrder = append(actorOrder, intent.Actor)
		}
		if previous, ok := winner[intent.Actor]; !ok ||
			intent.ArrivalID > q.Intents[previous].ArrivalID {
			winner[intent.Actor] = i
		}
	}
	for i := range q.Intents {
		if winner[q.Intents[i].Actor] != i {
			q.Results[i].Superseded = true
		}
	}
	for _, actor := range actorOrder {
		// ── 带序号的操作（客户端发来的）：全部 kind 进**同一条**队列，按 seq 保序 ──
		// 每次入队做一次插入排序（按 Seq），这样乱序到达的补齐包也能落到正确位置。
		legacy := -1
		queued := false
		for j := range q.Intents {
			if q.Intents[j].Actor != actor {
				continue
			}
			if q.Intents[j].Seq == 0 {
				// 无序号（旧客户端/内部测试/AI 之外的直接调用）：没有编号就无法参与
				// "按序号对齐"的和解，维持旧语义 —— 立即生效（最后一条赢），不进流、不推 ACK。
				legacy = j
				continue
			}
			if q.Pending == nil {
				q.Pending = make(map[ecs.Entity][]pendingOp)
			}
			if q.OpDriven == nil {
				q.OpDriven = make(map[ecs.Entity]struct{})
			}
			q.OpDriven[actor] = struct{}{} // 有 seq = 操作驱动（见 OpDriven 的说明）
			if !hasSeq(q.Pending[actor], q.Intents[j].Seq) {
				// 去重：冗余重发的包（还在排队里的那条）直接忽略
				q.Pending[actor] = insertBySeq(q.Pending[actor], pendingOp{Intent: q.Intents[j], ResultIndex: j})
			}
			// 队列上限：超出丢**最旧**并计数。宁可丢操作（客户端会从权威状态重新对齐），
			// 也不要让它无界增长 —— 积压会让这个玩家的每个操作都越来越晚。
			if limit := q.maxPending(); len(q.Pending[actor]) > limit {
				q.Pending[actor] = q.Pending[actor][len(q.Pending[actor])-limit:]
				q.Dropped++
			}
			// 入队 = 还没被消费：既不算 Superseded（没丢），也不算 Accepted（还没生效）
			q.Results[j] = ControlResult{Intent: q.Intents[j], Pending: true}
			queued = true
		}

		// 移动步数预算：这一 tick 该给这个 Actor 跑几步（= 消费掉几条 Move 操作）。
		// ⚠️ 服务端**不假设**"一个 tick 一条操作"：客户端每 tick 一条是端上的采样，
		//    到了这里会被网络抖动挤成一堆 ⇒ 积压时按 K 条消费，**K 条 move 配 K 步**。
		if queued || len(q.Pending[actor]) > 0 {
			consumeOps(w, q, actor, q.stepBudget())
			consumedThisTick[actor] = true
		}
		if legacy >= 0 {
			accepted, reason := applyImmediate(w, q, q.Intents[legacy])
			q.Results[legacy] = ControlResult{Intent: q.Intents[legacy], Accepted: accepted, Reason: reason}
		}
	}

	// 本 tick 没有新操作的 Actor：排队中的操作继续推进（同样按 K 条预算）。
	if len(q.Pending) > 0 {
		pending := make([]ecs.Entity, 0, len(q.Pending))
		for actor := range q.Pending {
			if !consumedThisTick[actor] {
				pending = append(pending, actor)
			}
		}
		for _, actor := range pending {
			consumeOps(w, q, actor, q.stepBudget())
		}
	}

	// 操作驱动的 Actor：本 tick 没消费到操作 ⇒ **一步都不走**。
	//
	// 关键是放进**空条目**（key 在、步数为 0）：MoveSystem 的 participatesInRound 对"有表"的
	// 实体只跑到自己步数用完为止，于是不会替它走 round 0 的保留意图。没有这张表（AI/生物/旧客户端）
	// 的语义不变 —— 它们本来就该按 tick 自己走。
	for actor := range q.OpDriven {
		if _, ok := q.Steps[actor]; !ok {
			if q.Steps == nil {
				q.Steps = make(map[ecs.Entity][]components.MoveDir)
			}
			q.Steps[actor] = nil
		}
	}

	q.Intents = q.Intents[:0]
}

// consumeOps 按 **seq 顺序**消费该 Actor 排队的前若干条操作。
//
// 预算按"移动步数"给（maxSteps）：**每条 Move 花一步**（用它自己的方向），Action/Cancel 不花步。
// 这样"K 条操作配 K 步模拟"永远成立 —— 绝不出现"消费了但没走"，否则客户端"第 N 条操作之后的状态"
// 在两边就不是同一个东西了。预算用完就停，剩下的留到后面的 tick。
func consumeOps(w *ecs.World, q *ControlQueue, actor ecs.Entity, maxSteps int) {
	if maxSteps < 1 {
		maxSteps = 1
	}
	steps := 0
	consumed := 0
	maxOps := q.maxPending() // 每 tick 最多消费多少条（防"一堆 action 挤在一个 tick"把成本顶爆）
	for len(q.Pending[actor]) > 0 && consumed < maxOps {
		head := q.Pending[actor][0]
		intent := head.Intent

		// 这个 Actor 的第一条操作：以它为基线（客户端每 epoch 从 1 开始，但旧客户端/测试可能不同号）。
		// 之后就必须连续 —— 这是"两边操作流逐条对应"的前提。
		if q.consumed[actor] == 0 {
			q.consumed[actor] = intent.Seq - 1
		}

		// ── 按序号连续消费：缺口没补齐就等（乱序/丢包靠冗余窗口补）──
		if want := q.consumed[actor] + 1; intent.Seq != want {
			q.gapWait[actor]++
			if q.gapWait[actor] < maxGapWaitTicks {
				break // 本 tick 不消费这个 Actor（等缺口）
			}
			// 等太久（超出冗余窗口）：跳过缺口并记账，客户端会因权威不同而校正回来
			q.Desyncs++
		}
		q.gapWait[actor] = 0

		if intent.Kind == ControlMove && steps >= maxSteps {
			break // 步数预算用完：Move 必须配步，绝不能"消费了不走"
		}

		accepted, reason := applyImmediate(w, q, intent)
		if intent.Kind == ControlMove && accepted {
			steps++
			if q.Steps == nil {
				q.Steps = make(map[ecs.Entity][]components.MoveDir)
			}
			dir := components.MoveDir{DX: intent.DX, DY: intent.DY}
			if len(intent.Path) > 0 {
				dir = intent.Path[0]
			}
			q.Steps[actor] = append(q.Steps[actor], dir)
		}
		consumed++

		q.consumed[actor] = intent.Seq
		q.Consumed = append(q.Consumed, ConsumedOp{
			Entity: actor, Seq: intent.Seq, Kind: intent.Kind, Accepted: accepted, Reason: reason,
		})

		// 回填 Results：这条才是本 tick 真正生效的那条。
		// 用下标 + ArrivalID 核对（O(1)）—— 跨 tick 消费时 Results 是新的，核对不过就跳过；
		// 不这样做就得每消费一条扫一遍 Results，多人多指令时是平方级。
		if i := head.ResultIndex; i >= 0 && i < len(q.Results) &&
			q.Results[i].Intent.ArrivalID == intent.ArrivalID {
			r := &q.Results[i]
			r.Accepted, r.Superseded, r.Pending, r.Reason = accepted, false, false, reason
		}

		if len(q.Pending[actor]) == 1 {
			delete(q.Pending, actor)
		} else {
			q.Pending[actor] = q.Pending[actor][1:]
		}
	}
	if steps > 1 {
		q.CatchupExtra += steps - 1
	}
}

// ResetActor 清掉某个 Actor 的待消费队列与序号记账（换输入世代/重连时调用）。
func (q *ControlQueue) ResetActor(actor ecs.Entity) {
	delete(q.Pending, actor)
	delete(q.Steps, actor)
	delete(q.consumed, actor)
	delete(q.gapWait, actor)
	delete(q.OpDriven, actor) // 不再是操作驱动：回到"按 tick 自己走"的语义
}

// Backlog 返回所有 Actor 待消费操作数之和与单个 Actor 的最大值（观测出口）。
func (q *ControlQueue) Backlog() (total, max int) {
	for _, list := range q.Pending {
		total += len(list)
		if len(list) > max {
			max = len(list)
		}
	}
	return total, max
}

// applyImmediate 应用一条操作（不进队列，立即生效）。移动/动作/取消共用。
func applyImmediate(w *ecs.World, q *ControlQueue, intent ControlIntent) (bool, ControlRejectReason) {
	switch intent.Kind {
	case ControlMove:
		return acceptMove(w, intent)
	case ControlStartAction:
		return acceptAction(w, q, intent)
	case ControlCancelAction:
		return acceptCancel(w, intent)
	default:
		return false, ControlRejectedUnsupportedAction
	}
}

// hasSeq 队列里是否已有这条序号（冗余重发去重用）。
func hasSeq(list []pendingOp, seq uint64) bool {
	for i := range list {
		if list[i].Intent.Seq == seq {
			return true
		}
	}
	return false
}

// insertBySeq 按 Seq 升序插入（队列很短，插入排序足够；乱序补齐包也能落回正确位置）。
func insertBySeq(list []pendingOp, op pendingOp) []pendingOp {
	i := len(list)
	for i > 0 && list[i-1].Intent.Seq > op.Intent.Seq {
		i--
	}
	list = append(list, pendingOp{})
	copy(list[i+1:], list[i:])
	list[i] = op
	return list
}

func acceptMove(w *ecs.World, intent ControlIntent) (bool, ControlRejectReason) {
	if !w.IsAlive(intent.Actor) || !ecs.Has[components.Position](w, intent.Actor) {
		return false, ControlRejectedInvalidActor
	}
	if ecs.Has[components.ActionState](w, intent.Actor) &&
		ecs.Get[components.ActionState](w, intent.Actor).Uninterruptible {
		return false, ControlRejectedBusy
	}
	moving := len(intent.Path) > 0 || intent.DX != 0 || intent.DY != 0
	if moving {
		components.TryInterrupt(w, intent.Actor, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_MOVED)
	}
	mv := ecs.Ensure[components.Moveable](w, intent.Actor)
	if mv.Speed <= 0 {
		mv.Speed = 10
	}
	if mv.EffectiveSpeed <= 0 {
		mv.EffectiveSpeed = mv.Speed
	}
	if len(intent.Path) > 0 {
		mv.DirX, mv.DirY = 0, 0
		mv.Path = append(mv.Path[:0], intent.Path...)
	} else {
		mv.Path = nil
		mv.DirX, mv.DirY = clampControlDir(intent.DX), clampControlDir(intent.DY)
	}
	ecs.MarkDirty[components.Moveable](w, intent.Actor)
	return true, ControlRejectedNone
}

func acceptAction(w *ecs.World, q *ControlQueue, intent ControlIntent) (bool, ControlRejectReason) {
	q.nextActionID++
	actionID := q.nextActionID
	reject := func(controlReason ControlRejectReason, outcomeReason game.ActionOutcomeReason) (bool, ControlRejectReason) {
		components.EmitActionOutcome(w, intent.Actor, components.ActionState{
			ActionID: actionID, Kind: intent.ActionKind, TargetEntity: intent.Target, RequestID: intent.RequestID,
		}, game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_REJECTED, outcomeReason)
		return false, controlReason
	}
	if !w.IsAlive(intent.Actor) || ecs.Has[components.Offline](w, intent.Actor) {
		return reject(ControlRejectedInvalidActor, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_ACTOR)
	}
	if ecs.Has[components.ActionState](w, intent.Actor) {
		return reject(ControlRejectedBusy, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_BUSY)
	}
	executors := ecs.Resource[ActionExecutorRegistry](w)
	executor, policy, ok := executors.ResolveDefinition(intent.ActionKind)
	if !ok {
		return reject(ControlRejectedUnsupportedAction, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSUPPORTED)
	}
	if ecs.Has[components.Dead](w, intent.Actor) && !policy.AllowWhenDead {
		return reject(ControlRejectedInvalidActor, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_ACTOR)
	}
	if reason := executor.Validate(w, intent.Actor, intent.Target); reason != ControlRejectedNone {
		return reject(reason, outcomeReasonForRejection(reason))
	}
	var timing ActionTiming
	if timer, contextual := executor.(contextualActionTimer); contextual {
		timing, ok = timer.TimingFor(w, intent.Actor, intent.Target, intent.Duration)
	} else {
		timing, ok = executor.Timing(intent.Duration)
	}
	if !ok {
		return reject(ControlRejectedUnsupportedAction, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSUPPORTED)
	}
	if intent.ActionKind == components.ActionCraft && !holdCraftIngredients(w, intent) {
		return reject(ControlRejectedInvalidTarget, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET)
	}

	stopMoving(w, intent.Actor)
	now := int64(worldPhase(w))
	ecs.Add(w, intent.Actor, components.ActionState{
		ActionID:        actionID,
		Kind:            intent.ActionKind,
		TargetEntity:    intent.Target,
		RequestID:       intent.RequestID,
		Phase:           components.ActionWindup,
		PhaseStartTick:  now,
		PhaseEndTick:    now + timing.Windup,
		CommitTick:      now + timing.Windup,
		EndTick:         now + timing.Windup + timing.Recovery,
		Uninterruptible: policy.Uninterruptible,
		// 投掷的落点随动作存活（windup 期间可能变化的世界状态下，
		// Commit 时仍要知道"往哪扔"）。其他动作 HasAim=false。
		HasAim: intent.HasAim,
		AimX:   intent.AimX,
		AimY:   intent.AimY,
	})
	components.RecordActionMetric(w, components.ActionMetricStarted, intent.ActionKind, 0)
	if ecs.Has[components.AI](w, intent.Actor) {
		ai := ecs.Get[components.AI](w, intent.Actor)
		if intent.ActionKind == components.ActionAttack {
			ai.Cooldown = weaponOf(w, intent.Actor).AttackCooldown
		}
		ecs.MarkDirty[components.AI](w, intent.Actor)
	}
	return true, ControlRejectedNone
}

func holdCraftIngredients(w *ecs.World, intent ControlIntent) bool {
	if intent.RecipeID == "" || len(intent.Ingredients) == 0 ||
		!ecs.Has[components.Inventory](w, intent.Actor) {
		return false
	}
	inv := ecs.Get[components.Inventory](w, intent.Actor)
	held := make([]components.ItemStack, 0, len(intent.Ingredients))
	heldIndex := make(map[components.ItemKind]int, len(intent.Ingredients))
	for _, ingredient := range intent.Ingredients {
		if ingredient.Count <= 0 {
			return false
		}
		if i, ok := heldIndex[ingredient.Kind]; ok {
			held[i].Count += ingredient.Count
		} else {
			heldIndex[ingredient.Kind] = len(held)
			held = append(held, ingredient)
		}
	}
	for _, ingredient := range held {
		if inv.CountOf(ingredient.Kind) < ingredient.Count {
			return false
		}
	}
	for _, ingredient := range held {
		if !inv.Take(ingredient.Kind, ingredient.Count) {
			return false
		}
	}
	ecs.MarkDirty[components.Inventory](w, intent.Actor)
	ecs.Add(w, intent.Actor, components.Crafting{
		RecipeID:    intent.RecipeID,
		TicksLeft:   int(intent.Duration),
		Ingredients: held,
	})
	return true
}

func acceptCancel(w *ecs.World, intent ControlIntent) (bool, ControlRejectReason) {
	if !w.IsAlive(intent.Actor) {
		return false, ControlRejectedInvalidActor
	}
	if ecs.Has[components.ActionState](w, intent.Actor) &&
		ecs.Get[components.ActionState](w, intent.Actor).Uninterruptible {
		return false, ControlRejectedBusy
	}
	if !ecs.Has[components.ActionState](w, intent.Actor) && !ecs.Has[components.Crafting](w, intent.Actor) {
		return false, ControlRejectedBusy
	}
	components.TryInterrupt(w, intent.Actor, game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT)
	return true, ControlRejectedNone
}

func stopMoving(w *ecs.World, actor ecs.Entity) {
	if !ecs.Has[components.Moveable](w, actor) {
		return
	}
	mv := ecs.Get[components.Moveable](w, actor)
	if mv.DirX == 0 && mv.DirY == 0 && len(mv.Path) == 0 {
		return
	}
	mv.DirX, mv.DirY = 0, 0
	mv.Path = nil
	ecs.MarkDirty[components.Moveable](w, actor)
}

func clampControlDir(v int) int {
	if v > 0 {
		return 1
	}
	if v < 0 {
		return -1
	}
	return 0
}

type ActionTiming struct {
	Windup, Recovery int64
}

func outcomeReasonForRejection(reason ControlRejectReason) game.ActionOutcomeReason {
	switch reason {
	case ControlRejectedInvalidActor:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_ACTOR
	case ControlRejectedBusy:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_BUSY
	case ControlRejectedInvalidTarget:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET
	case ControlRejectedUnsupportedAction:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSUPPORTED
	default:
		return game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSPECIFIED
	}
}

// ActionCommit 是已成功提交、需要世界适配层补齐副作用的语义记录。
type ActionCommit struct {
	Actor  ecs.Entity
	Target ecs.Entity
	Kind   components.ActionKind
}

type ActionCommitQueue struct {
	Commits []ActionCommit
}

func DrainActionCommits(w *ecs.World) []ActionCommit {
	q := ecs.Resource[ActionCommitQueue](w)
	out := append([]ActionCommit(nil), q.Commits...)
	q.Commits = q.Commits[:0]
	return out
}

// ActionSystem 推进 phase，并对同 tick due actions 先冻结集合再统一 commit。
type ActionSystem struct{}

type dueAction struct {
	actor ecs.Entity
	state components.ActionState
}

func (s *ActionSystem) Update(w *ecs.World, dt time.Duration) {
	now := int64(worldPhase(w))
	due := make([]dueAction, 0)
	ecs.Query[components.ActionState](w, func(e ecs.Entity, state *components.ActionState) {
		if state.Phase == components.ActionWindup && now >= state.CommitTick {
			due = append(due, dueAction{actor: e, state: *state})
		}
	})
	sort.Slice(due, func(i, j int) bool {
		return due[i].actor < due[j].actor
	})

	results := make(map[uint64]ActionCommitResult, len(due))
	executors := ecs.Resource[ActionExecutorRegistry](w)
	// due 集合已经冻结；结算过程中产生的血量变化不会改变本批候选集合。
	for _, action := range due {
		executor, ok := executors.Resolve(action.state.Kind)
		if !ok {
			continue
		}
		result := executor.Commit(w, action.actor, action.state)
		results[action.state.ActionID] = result
		if result.Committed {
			components.RecordActionMetric(w, components.ActionMetricCommitted, action.state.Kind, 0)
		}
	}
	for _, action := range due {
		if !w.IsAlive(action.actor) || !ecs.Has[components.ActionState](w, action.actor) {
			continue
		}
		state := ecs.Get[components.ActionState](w, action.actor)
		if state.ActionID != action.state.ActionID || state.Phase != components.ActionWindup {
			continue
		}
		result := results[state.ActionID]
		if !result.Committed &&
			result.FailureReason != game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSPECIFIED {
			components.EmitActionOutcome(
				w, action.actor, *state,
				game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_CANCELED,
				result.FailureReason,
			)
			ecs.Remove[components.ActionState](w, action.actor)
			continue
		}
		if result.CompleteImmediately {
			components.CompleteAction(w, action.actor)
			continue
		}
		if result.Committed && result.RepeatAfter > 0 {
			state.Phase = components.ActionWindup
			state.PhaseStartTick = now
			state.PhaseEndTick = now + result.RepeatAfter
			state.CommitTick = now + result.RepeatAfter
			state.EndTick = now + result.RepeatAfter
			ecs.MarkDirty[components.ActionState](w, action.actor)
			continue
		}
		state.Phase = components.ActionRecovery
		state.PhaseStartTick = now
		state.PhaseEndTick = state.EndTick
		// 投掷的**抛出阶段不可打断**（箭已离弦）。
		//
		// 两段语义：windup（手里）可被打断——动作没做完就不该飞出去；
		// 一旦 Commit 完成（物体已离手），再打断也无法收回，反而会让
		// 客户端看到一个"被取消但东西已经飞了"的矛盾表现。
		// 所以在进入 recovery 的那一刻把动作标记为不可打断。
		if state.Kind == components.ActionThrow {
			state.Uninterruptible = true
		}
		ecs.MarkDirty[components.ActionState](w, action.actor)
	}

	var completed []ecs.Entity
	ecs.Query[components.ActionState](w, func(e ecs.Entity, state *components.ActionState) {
		if state.Phase == components.ActionRecovery && now >= state.EndTick {
			completed = append(completed, e)
		}
	})
	sort.Slice(completed, func(i, j int) bool { return completed[i] < completed[j] })
	for _, e := range completed {
		if w.IsAlive(e) && ecs.Has[components.ActionState](w, e) {
			components.CompleteAction(w, e)
		}
	}
}
