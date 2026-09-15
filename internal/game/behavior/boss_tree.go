package behavior

// 本文件是 **Boss 三阶段行为树**：行为树的"压力测试用例"。
//
// 设计目标（用户需求）：
//
//	阶段一：只投炸弹攻击
//	阶段二：血量掉到一半时进入 —— 先嚎叫一次，然后瞬间跳到玩家面前，
//	        开始打拳；**每打三拳就锤一次地面放 AOE**
//
// # 阶段是怎么表达的
//
// 阶段存在黑板（AI 组件）里，是**持久状态**而不是树的形状。树每 tick
// 从根重新评估，靠 `PhaseIs` 条件分流到不同阶段的分支：
//
//	Selector（根）
//	├── [阶段推进] 血量过半 且 还在阶段一 → 切到阶段二
//	├── [阶段二] PhaseIs(2)
//	│   ├── 未嚎叫过 → 嚎叫（Once 保证只一次）
//	│   ├── 还没贴脸   → 闪现到玩家身边
//	│   └── 连招       → Counter(3, 打拳, 锤地AOE)
//	└── [阶段一] 投弹 / 追击 / 待机
//
// # 为什么"阶段推进"要放在最前面
//
// Selector 是从上往下第一个能做的赢。把阶段切换放在最前，保证"血量一旦
// 过半就立刻换阶段"，而不会因为当前 tick 恰好走进了阶段一的分支而延迟。

// BossConfig 是 Boss 行为的可调参数（数值不写死在树结构里，方便调参）。
type BossConfig struct {
	// Phase2HP 进入二阶段的血量阈值（血 <= 此值切阶段二）。
	Phase2HP int
	// RoarTicks 嚎叫持续时间（tick）。
	RoarTicks int
	// SlamTicks AOE 前摇时间（tick）。
	SlamTicks int
	// PunchesPerSlam 几拳之后接一次 AOE（默认 3）。
	PunchesPerSlam int
	// MeleeRange 认为"已经贴脸、可以开始打拳"的距离（格）。
	//
	// 取值必须 >= 2：闪现的落点是目标的**相邻格**（见 systems.leapLanding），
	// 对角相邻的曼哈顿距离是 2。若设成 1，闪现落地后仍不满足"贴脸"，
	// 就会每 tick 反复闪现（实测踩过：Boss 在原地疯狂闪烁、永不挥拳）。
	MeleeRange int
	// ThrowRange 投弹的期望距离上限（超过则先接近）。
	ThrowRange int
}

// DefaultBossConfig 返回缺省参数（20Hz tick）。
func DefaultBossConfig() BossConfig {
	return BossConfig{
		Phase2HP:       200,
		RoarTicks:      30, // 1.5s
		SlamTicks:      20, // 1.0s
		PunchesPerSlam: 3,
		MeleeRange:     2, // 必须 >= 2，见 MeleeRange 注释
		ThrowRange:     12,
	}
}

// BossTree 构造 Boss 行为树。
//
// 参数化的地方：所有时长/次数都来自 cfg，树结构本身固定。
// 想调"几拳一砸"只改配置，不用动树。
func BossTree(cfg BossConfig) *Tree {
	punches := cfg.PunchesPerSlam
	if punches <= 0 {
		punches = 3
	}
	return NewTree(
		NewSelector(
			// ① 阶段推进：血量过半且在阶段一 → 进入阶段二。
			//    放在最前，保证"一旦过半立刻换阶段"，不被阶段一分支抢占。
			NewSequence(
				newPhaseOnePrefix(),
				&Phase2Ready{},
				NewEnterPhase(2),
			),
			// ② 阶段二：嚎叫 → 闪现贴脸 → 三拳一砸
			NewSequence(
				NewPhaseIs(2),
				NewSelector(
					// ②-a 进场嚎叫，整个生命周期只做一次
					NewOnce(NewRoar(cfg.RoarTicks)),
					// ②-b 还没贴脸：瞬间位移到玩家身边
					NewSequence(
						&HasTarget{},
						NewInverter(NewHasTargetInRange(cfg.MeleeRange)),
						&LeapToTargetAction{},
					),
					// ②-c 已经贴脸：连招（打三拳 → 锤地 AOE → 重新数）
					NewSequence(
						&HasTarget{},
						NewCounter(punches, &PunchAction{}, NewSlamAOE(cfg.SlamTicks)),
					),
					// ②-d 没有目标：回防待机
					newIdleBranch(),
				),
			),
			// ③ 阶段一：只投炸弹（有目标就投，没目标就游荡）
			NewSelector(
				// 目标太远：先接近到投弹距离
				NewSequence(
					&HasTarget{},
					NewInverter(NewHasTargetInRange(cfg.ThrowRange)),
					&ChaseAction{},
				),
				// 目标在投弹距离内：投弹
				NewSequence(
					&HasTarget{},
					&ThrowBombAction{},
				),
				// 兜底：回防 / 游荡
				newIdleBranch(),
			),
		),
	)
}

// newPhaseOnePrefix 是"仍在阶段一"的判断。
//
// 阶段一的判定写成"不是阶段二"而不是"== 1"：因为初始阶段是 0
// （未显式设置过），用 `!= 2` 能同时覆盖"初始未设"和"显式设为 1"两种情况。
type phaseNotTwo struct{ nodeBase }

func newPhaseOnePrefix() Node { return &phaseNotTwo{} }

func (phaseNotTwo) Children() []Node { return nil }

func (phaseNotTwo) Tick(d *TickContext, _ NodeID) Status {
	if d.Board.Phase() == 2 {
		return Failure
	}
	return Success
}

// newIdleBranch 是各阶段共用的兜底分支：超出游荡半径回防，否则游荡。
func newIdleBranch() Node {
	return NewSelector(
		NewSequence(&OutOfRoam{}, &ReturnHomeAction{}),
		NewWander(24),
	)
}
