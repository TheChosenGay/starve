package behavior

// 本文件是**内置生物行为树**：把 AISystem 原先硬编码的 4 状态状态机
// （idle / chase / attack / flee）表达成行为树。
//
// 语义必须与原 switch **完全等价**（现有 creature 测试是回归保险）：
//
//	原逻辑（按优先级）：
//	  无目标            → idle
//	  无攻击力 或 低血  → flee
//	  目标在攻击范围    → attack
//	  否则              → chase
//
// 行为树写法（Selector 从上往下试，第一个能做的赢）：
//
//	Selector
//	├── Sequence  逃跑：低血 或 无攻击力 + 有目标 → flee
//	├── Sequence  攻击：有目标 + 能攻击 + 在范围 → attack
//	├── Sequence  追击：有目标 + 能攻击          → chase
//	└── 游荡/待机                                 → idle
//
// 注意"无攻击力"的处理：原逻辑里被动生物（兔/鹿）只要**有目标**就直接逃，
// 不看血量。等价写法是 Inverter(CanAttack) 而非 LowHP，两者都要在逃跑分支里。

// PredatorTree 掠食者行为树（狼/野猪/蜘蛛这类能攻击的生物）。
//
//	逃跑：低血
//	攻击：有目标 + 在范围
//	追击：有目标
//	兜底：游荡 / 回防
func PredatorTree() *Tree {
	return NewTree(NewSelector(
		// ① 低血逃跑
		NewSequence(&LowHP{}, &FleeAction{}),
		// ② 攻击（冷却由 Cooldown 装饰器统一节流）
		NewSequence(&HasTarget{}, &InAttackRange{}, NewCooldown(0, &AttackAction{})),
		// ③ 追击
		NewSequence(&HasTarget{}, &ChaseAction{}),
		// ④ 兜底：超出游荡半径回防，否则游荡
		NewSelector(
			NewSequence(&OutOfRoam{}, &ReturnHomeAction{}),
			NewWander(24),
		),
	))
}

// PreyTree 被动生物行为树（兔/鹿这类无攻击力生物）。
//
//	逃跑：有目标（被打后一定会锁定攻击者）
//	兜底：游荡 / 回防
//
// 与 PredatorTree 的差别：逃跑分支只看"有没有目标"，不看血量
// （对应原逻辑 `wp.AttackDamage <= 0 || 低血`）。
func PreyTree() *Tree {
	return NewTree(NewSelector(
		// ① 有威胁就跑（被动生物不还手）
		NewSequence(&HasTarget{}, &FleeAction{}),
		// ② 兜底：回防 / 游荡
		NewSelector(
			NewSequence(&OutOfRoam{}, &ReturnHomeAction{}),
			NewWander(24),
		),
	))
}

// DormantTree 完全被动的行为树（只会游荡，不追不逃）。
// 供 scenery 类生物或调试用。
func DormantTree() *Tree {
	return NewTree(NewSelector(
		NewSequence(&OutOfRoam{}, &ReturnHomeAction{}),
		NewWander(24),
	))
}

// TreeForCreature 按"能否攻击"选择内置树。
//
// 这是过渡期的工厂：等行为树支持配置驱动后，改为按 creature kind 查表。
func TreeForCreature(canAttack bool) *Tree {
	if canAttack {
		return PredatorTree()
	}
	return PreyTree()
}
