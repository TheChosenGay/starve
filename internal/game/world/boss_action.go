package world

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/systems"
	"starve/internal/game/world/behavior"
)

// BossActionSystem 消费行为树产出的 Boss 动作**意图**（order 99），把它变成真实效果。
//
// # 为什么需要这个系统
//
// 行为树是纯决策层：它只 `components.EmitBossAction(...)`，不改位置/血量（见
// components/boss_action.go 的说明）。此前**真实世界层没有任何消费者**，只有
// cmd/bossdemo 演示自己消费一份 ⇒ 正式玩法里 Boss 的投弹/锤地全是空放
// （行为树以为放了技能，世界什么也没发生）。本系统是真实世界侧唯一的消费者。
//
// # 为什么放在 order 99
//
// 95~98 已被 Move(95)/DebugShape(96)/CreatureOccupancy(97)/Throw(98) 占满，
// 而 ECS 对 order 冲突是**报错**而不是覆盖。放 98 之后意味着本 tick 生成的炸弹
// 从**下一 tick** 才开始飞（50ms，肉眼不可见），换来的是不打乱既有顺序。
//
// # 各类动作的分工（重要：一半动作在别处已经有真实效果）
//
//   - ThrowBomb：**本系统实现**——在 Boss 脚下实体化一颗炸弹，再走与玩家完全相同的
//     ThrowBehavior 投掷路径（校验力量/距离/落点），落地由 ThrowSystem 引爆；
//   - Slam：**本系统实现**——以 Boss 为圆心的半球 AOE 伤害 + 击退 + BlastEvent 广播；
//   - Leap：**已在 btEnv.LeapTo 完成**（真位移 + 落点校验 + 标脏 Position/Moveable）。
//     闪现是原子操作、没有中间态，这里再动一次位置只会把已校验的落点覆盖掉；
//   - Punch：**已在 btEnv.Punch 里 StartAttack**，伤害在动作 commit 阶段由 AttackBehavior
//     结算（走权威动作时间轴，客户端能看到起手/命中）。这里直接扣血反而会绕过冷却与表现；
//   - Roar：只是"进入下一阶段"的标记，阶段判定本身在行为树/血量阈值里，无需世界层副作用。
//
// 后三类仍会被 Drain 取走（队列是 tick 语义），不会被重复应用。
type BossActionSystem struct {
	a *WorldActor
}

// NewBossActionSystem 造一个 Boss 动作消费者（世界层装配时注册一次）。
func NewBossActionSystem(a *WorldActor) *BossActionSystem {
	return &BossActionSystem{a: a}
}

// Update 实现 ECS 系统接口：每 tick 取走并应用本 tick 的 Boss 动作意图。
func (s *BossActionSystem) Update(w *ecs.World, _ time.Duration) {
	if s == nil || s.a == nil {
		return
	}
	for _, act := range components.DrainBossActions(w) {
		s.apply(w, act)
	}
}

// apply 把一条意图分派到具体效果。
func (s *BossActionSystem) apply(w *ecs.World, act components.BossAction) {
	switch act.Kind {
	case components.BossActionThrowBomb:
		s.throwBomb(w, act)
	case components.BossActionSlam:
		s.slam(w, act)
	default:
		// Leap/Punch/Roar 的真实效果在别处（见类型注释），这里刻意什么都不做。
		// 不写 log：Punch 每个攻击周期都会来一条，写日志会把 Boss 的行为流水刷满。
	}
}

// throwBomb 把"投弹意图"变成一颗**真的**炸弹：Boss 脚下实体化一枚炸弹，再走统一投掷路径。
//
// 为什么不复用 materializeOneForThrow：那个入口从**玩家背包**取物品，Boss 没有背包，
// 也不该为了投弹给它塞一个（那会让 Boss 变成"会掉炸弹的容器"）。
// 炸弹必须是**生成**出来的，并且必须走 SpawnItemEntity —— 只有它会按模板挂上
// Throwable/Explosive；少一样都会被 ThrowBehavior 在前置校验里拒掉。
//
// 失败（力量不足/超出距离/落点不可站）必须**销毁**这颗炸弹：否则 Boss 每次尝试投弹
// 都在脚下留一颗炸弹，地上很快堆满"免费炸弹"，玩家捡了就能反过来炸 Boss。
func (s *BossActionSystem) throwBomb(w *ecs.World, act components.BossAction) {
	if act.Actor == 0 || !w.IsAlive(act.Actor) {
		return
	}
	if act.Target == 0 || !w.IsAlive(act.Target) {
		return
	}
	bossPos, ok := entityPosition(w, act.Actor)
	if !ok {
		return
	}
	targetPos, ok := entityPosition(w, act.Target)
	if !ok {
		return
	}

	bomb := s.a.SpawnItemEntity(bossPos.X, bossPos.Y, components.ItemStack{Kind: components.ItemBomb, Count: 1})
	if bomb == 0 {
		return
	}

	res := behavior.ThrowBehavior{}.Throw(w, act.Actor, behavior.ThrowRequest{
		Thrown: bomb,
		FromX:  float64(bossPos.X),
		FromY:  float64(bossPos.Y),
		ToX:    float64(targetPos.X),
		ToY:    float64(targetPos.Y),
	})
	if !res.Success {
		w.DestroyEntity(bomb)
	}
}

// slam 锤地 AOE：以 Boss 为圆心的半球范围伤害 + 击退 + 广播。
//
// 伤害取**攻击者自己的攻击力**（interactive.Attacker.AttackDamage），与近战同源：
// 否则"怪的攻击力配一处、AOE 伤害配另一处"，调参时必然漂移。
//
// 广播用 BlastEvent 且 thrown=0 —— 协议里这个字段的注释就是"0 = 非投掷物，如 Boss 锤地"
// （见 proto BlastEvent）。客户端据此画扩散圈/震屏，**不**反推伤害：
// 伤害已经逐个通过 ApplyDamage → HealthChanged 下发。
//
// 击退强度用 DefaultBlastKnockback：与炸弹一致，不额外配一个"锤地击退"参数。
func (s *BossActionSystem) slam(w *ecs.World, act components.BossAction) {
	if act.Actor == 0 || !w.IsAlive(act.Actor) {
		return
	}
	bossPos, ok := entityPosition(w, act.Actor)
	if !ok {
		return
	}
	radius := float64(act.Radius)
	if radius <= 0 {
		return
	}

	centerX, centerY := float64(bossPos.X), float64(bossPos.Y)
	damage := attackerDamage(w, act.Actor)

	for _, hit := range systems.BlastTargets(w, centerX, centerY, radius) {
		if hit.Entity == act.Actor {
			continue // 不砸自己
		}
		if damage > 0 && ecs.Has[components.Attackable](w, hit.Entity) && ecs.Has[components.Health](w, hit.Entity) {
			// attacker = Boss：目标会记直接仇恨，并向同类传播（与近战一致）。
			components.Attackable{}.ApplyDamage(w, hit.Entity, act.Actor, damage)
		}
		systems.BlastKnockback(w, centerX, centerY, radius, components.DefaultBlastKnockback, hit)
	}

	components.EmitBlast(w, act.Actor, 0, centerX, centerY, radius, damage)
}

// entityPosition 取实体的整格位置（没有 Position 组件时返回 false）。
func entityPosition(w *ecs.World, e ecs.Entity) (components.Position, bool) {
	if !ecs.Has[components.Position](w, e) {
		return components.Position{}, false
	}
	return *ecs.Get[components.Position](w, e), true
}

// attackerDamage 取实体的攻击力（自身能力或手持装备，走 ActorCap 统一口径）；
// 没有攻击能力时返回 0 —— 此时 AOE 只击退不伤害，而不是崩掉。
func attackerDamage(w *ecs.World, e ecs.Entity) int {
	_, atk := interactive.ActorCap[interactive.Attacker](w, e)
	if atk == nil {
		return 0
	}
	return atk.AttackDamage
}
