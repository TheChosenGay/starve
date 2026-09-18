package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/config"
	"starve/internal/game/worldmap"
)

// Boss 动作的**世界层接线**测试。
//
// 背景（这是"意图没接上"的典型）：行为树只产出意图
// （components.EmitBossAction），此前真实世界层**没有消费者**，
// 只有 cmd/bossdemo 演示自己消费一份 ⇒ 正式玩法里 Boss 的投弹/锤地全是空放：
// 行为树以为放了技能，世界什么都没发生，日志也毫无异常。
//
// 这里验证的就是"接线"本身：意图 → 真实效果（炸弹真的飞/真的炸、AOE 真的扣血）。
// 注意 Boss **本体**（生物枚举/模板/生成点）目前还不存在，属于另一步；
// 测试用"能力齐备的 Boss 实体"来表达接线契约。

// bossActionTestBoss 造一个能近战、能投掷的 Boss 实体。
//
// 能力用显式组件给出，而不是走 creatures.json：Boss 模板还没落地，
// 而接线测试关心的是"有 Thrower/Attacker 的 Boss 能不能把意图变成效果"。
func bossActionTestBoss(t *testing.T, wa *WorldActor, x, y int) ecs.Entity {
	t.Helper()
	boss := wa.sim.CreateEntity()
	ecs.Add(wa.sim, boss, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, boss, components.Health{Cur: 500, Max: 500})
	ecs.Add(wa.sim, boss, components.Attackable{})
	ecs.Add(wa.sim, boss, interactive.Attacker{AttackDamage: 12, AttackRange: 1, AttackCooldown: 40})
	// 力量 60 ⇒ 最大投掷距离 = 8 × 60 / 18 ≈ 26 格（炸弹质量 18）。
	ecs.Add(wa.sim, boss, interactive.Thrower{Strength: 60})
	return boss
}

// bossActionTestTarget 造一个能被伤害的静态目标（有 Health/Attackable/Position）。
func bossActionTestTarget(t *testing.T, wa *WorldActor, x, y, hp int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: hp, Max: hp})
	ecs.Add(wa.sim, e, components.Attackable{})
	return e
}

func healthOf(t *testing.T, wa *WorldActor, e ecs.Entity) int {
	t.Helper()
	if !ecs.Has[components.Health](wa.sim, e) {
		t.Fatalf("实体 %d 没有 Health 组件", e)
	}
	return ecs.Get[components.Health](wa.sim, e).Cur
}

// countThrown 返回当前带着 Thrown 的实体（飞行中的投掷物）。
func countThrown(wa *WorldActor) []ecs.Entity {
	var out []ecs.Entity
	ecs.Query[components.Thrown](wa.sim, func(e ecs.Entity, _ *components.Thrown) {
		out = append(out, e)
	})
	return out
}

// drainBlasts 取走本 tick 的爆炸事件（用于断言"客户端能看到爆炸表现"）。
func drainBlasts(wa *WorldActor) []*struct {
	Source uint64
	Thrown uint64
	X, Y   float64
	Radius float64
} {
	var out []*struct {
		Source uint64
		Thrown uint64
		X, Y   float64
		Radius float64
	}
	if buf, ok := ecs.TryResource[components.TickEventBuffer](wa.sim); ok {
		for _, ev := range buf.Events {
			if b := ev.GetBlast(); b != nil {
				out = append(out, &struct {
					Source uint64
					Thrown uint64
					X, Y   float64
					Radius float64
				}{b.SourceEntity, b.ThrownEntity, float64(b.X), float64(b.Y), float64(b.Radius)})
			}
		}
	}
	components.DrainTickEvents(wa.sim)
	return out
}

// 投弹意图必须变成**一颗真的炸弹**：挂 Thrown、飞向目标、落地爆炸并消耗掉。
//
// 这是"空放"最典型的回归：意图没消费者时，Boss 每次投弹都无事发生。
func TestBossThrowBombBecomesRealFlyingBomb(t *testing.T) {
	wa := newThrowTestWorld(t)
	boss := bossActionTestBoss(t, wa, 10, 4)
	target := bossActionTestTarget(t, wa, 10, 10, 100)

	components.EmitBossAction(wa.sim, components.BossActionThrowBomb, boss, target, 0)

	// ① 意图被消费：生成炸弹并进入飞行（order 99 在 Throw 98 之后，下一 tick 才推进）
	tickWorld(wa)
	thrown := countThrown(wa)
	if len(thrown) != 1 {
		t.Fatalf("投弹意图应产生 1 个飞行中的炸弹，实际 %d 个 —— "+
			"没有消费者时 Boss 投弹就是空放", len(thrown))
	}
	bomb := thrown[0]
	th := ecs.Get[components.Thrown](wa.sim, bomb)
	if th.Thrower != boss {
		t.Fatalf("炸弹的投掷者应为 Boss %d，实际 %d", boss, th.Thrower)
	}
	if th.ToX != 10 || th.ToY != 10 {
		t.Fatalf("落点应为目标格 (10,10)，实际 (%v,%v)", th.ToX, th.ToY)
	}
	if th.FlightTicks <= 0 {
		t.Fatalf("飞行时长应 > 0，实际 %d", th.FlightTicks)
	}
	// 炸弹必须带爆炸属性（来自物品模板），否则落地只是一个"停在原地的物品"
	if !ecs.Has[components.Explosive](wa.sim, bomb) {
		t.Fatal("Boss 的炸弹必须带 Explosive（否则落地不炸）")
	}

	// ② 落地：目标掉血、炸弹被消耗（销毁）、广播爆炸事件
	hpBefore := healthOf(t, wa, target)
	for i := 0; i <= th.FlightTicks+2; i++ {
		tickWorld(wa)
	}
	if got := healthOf(t, wa, target); got >= hpBefore {
		t.Fatalf("炸弹落地应炸伤目标：HP %d → %d", hpBefore, got)
	}
	if left := countThrown(wa); len(left) != 0 {
		t.Fatalf("落地后不应还有飞行物，实际 %d 个", len(left))
	}
	if !wa.sim.IsAlive(bomb) {
		// 期望：爆炸物落地即销毁（否则地上会留一颗可拾取的炸弹 = 无限炸弹）
	} else {
		t.Fatal("爆炸物落地后应被销毁（否则地上残留可拾取的炸弹）")
	}

	blasts := drainBlasts(wa)
	if len(blasts) == 0 {
		t.Fatal("落地应广播爆炸事件（否则客户端看不到爆炸表现）")
	}
	last := blasts[len(blasts)-1]
	if last.Thrown != uint64(bomb) {
		t.Fatalf("炸弹爆炸事件的 thrown 字段应为炸弹实体 %d，实际 %d", bomb, last.Thrown)
	}
	if last.Source != uint64(boss) {
		t.Fatalf("爆炸来源应为 Boss %d，实际 %d", boss, last.Source)
	}
}

// 超出投掷距离时必须**不留炸弹**：否则 Boss 每次尝试投弹都在脚下掉一颗，
// 地上很快堆满"免费炸弹"，玩家捡起来就能反过来炸 Boss。
func TestBossThrowBombOutOfRangeLeavesNoBomb(t *testing.T) {
	wa := newThrowTestWorld(t)
	boss := bossActionTestBoss(t, wa, 10, 10)
	// 力量 60 ⇒ 上限约 26 格；放 30 格外必然被 ThrowBehavior 拒掉。
	target := bossActionTestTarget(t, wa, 10, 40, 100)

	entitiesBefore := wa.sim.EntityCount()
	components.EmitBossAction(wa.sim, components.BossActionThrowBomb, boss, target, 0)
	tickWorld(wa)
	tickWorld(wa)

	if got := countThrown(wa); len(got) != 0 {
		t.Fatalf("超距投掷应被拒绝，实际有 %d 个飞行物", len(got))
	}
	if after := wa.sim.EntityCount(); after != entitiesBefore {
		t.Fatalf("投掷失败不应在世界里留下炸弹：实体数 %d → %d", entitiesBefore, after)
	}
}

// 锤地 AOE：只打半径内的目标、伤害等于 Boss 攻击力、击退、广播炸弹同族的爆炸事件。
func TestBossSlamHitsOnlyInsideRadius(t *testing.T) {
	wa := newThrowTestWorld(t)
	boss := bossActionTestBoss(t, wa, 10, 10)
	near := bossActionTestTarget(t, wa, 11, 10, 100) // 距离 1（半径 3 内）
	far := bossActionTestTarget(t, wa, 20, 10, 100)  // 距离 10（半径外）

	bossHPBefore := healthOf(t, wa, boss)
	nearHPBefore := healthOf(t, wa, near)
	components.EmitBossAction(wa.sim, components.BossActionSlam, boss, 0, 3)
	tickWorld(wa)

	if got, want := healthOf(t, wa, near), nearHPBefore-12; got != want {
		t.Fatalf("半径内目标应受到 Boss 攻击力(12)的伤害：HP %d → %d（期望 %d）",
			nearHPBefore, got, want)
	}
	if got := healthOf(t, wa, far); got != 100 {
		t.Fatalf("半径外目标不该被锤地波及：HP 100 → %d", got)
	}
	if got := healthOf(t, wa, boss); got != bossHPBefore {
		t.Fatalf("锤地不该伤到自己：HP %d → %d", bossHPBefore, got)
	}

	blasts := drainBlasts(wa)
	if len(blasts) != 1 {
		t.Fatalf("锤地应广播 1 条爆炸事件，实际 %d 条", len(blasts))
	}
	// thrown=0 是协议为"非投掷物（Boss 锤地）"预留的语义
	if blasts[0].Thrown != 0 {
		t.Fatalf("锤地事件的 thrown 应为 0，实际 %d", blasts[0].Thrown)
	}
	if blasts[0].Radius != 3 {
		t.Fatalf("锤地事件半径应为 3，实际 %v", blasts[0].Radius)
	}

	// 击退：目标必须被推离爆心（走碰撞滑动，不是直接改坐标）
	nearPos := ecs.Get[components.Position](wa.sim, near)
	if nearPos.X == 11 && nearPos.Y == 10 {
		t.Fatalf("半径内目标应被击退，实际仍停在 (%d,%d)", nearPos.X, nearPos.Y)
	}
	if nearPos.Manhattan(components.Position{X: 10, Y: 10}) <= 1 {
		t.Fatalf("击退方向应背离爆心（Boss），实际到 (%d,%d)", nearPos.X, nearPos.Y)
	}
}

// Leap/Punch/Roar 在**世界层**刻意没有副作用（真实效果在别处），
// 本测试把这条分工写死：避免以后有人在世界层又实现一遍，造成"闪现两次/出拳双倍伤害"。
func TestBossLeapPunchRoarHaveNoWorldLayerEffect(t *testing.T) {
	wa := newThrowTestWorld(t)
	boss := bossActionTestBoss(t, wa, 10, 10)
	target := bossActionTestTarget(t, wa, 11, 10, 100)

	entitiesBefore := wa.sim.EntityCount()
	hpBefore := healthOf(t, wa, target)

	components.EmitBossAction(wa.sim, components.BossActionLeap, boss, target, 0)
	components.EmitBossAction(wa.sim, components.BossActionPunch, boss, target, 0)
	components.EmitBossAction(wa.sim, components.BossActionRoar, boss, 0, 0)
	tickWorld(wa)
	tickWorld(wa) // 再跑一 tick：验证队列是 Drain 语义，不会重复应用

	if got := healthOf(t, wa, target); got != hpBefore {
		t.Fatalf("Leap/Punch/Roar 在世界层不应扣血（Punch 的伤害由 StartAttack 在 commit 结算）：%d → %d", hpBefore, got)
	}
	if after := wa.sim.EntityCount(); after != entitiesBefore {
		t.Fatalf("Leap/Punch/Roar 不应生成实体：%d → %d", entitiesBefore, after)
	}
	if blasts := drainBlasts(wa); len(blasts) != 0 {
		t.Fatalf("Leap/Punch/Roar 不应广播爆炸事件，实际 %d 条", len(blasts))
	}
	// 队列必须已空（意图被取走），否则下一 tick 会重复应用
	if left := components.DrainBossActions(wa.sim); len(left) != 0 {
		t.Fatalf("意图应被消费者取走，队列里还剩 %d 条", len(left))
	}
}

// 生物模板的 throw_strength 决定"这个生物会不会投掷"。
//
// 这是投弹接线的**前置条件**：ThrowBehavior 的前置校验要求投掷者带 interactive.Thrower，
// 而此前只有玩家会被挂上它 ⇒ 任何生物（包括 Boss）的投弹都必然在校验阶段被拒。
func TestCreatureThrowStrengthSeedsThrowerCapability(t *testing.T) {
	wa := newThrowTestWorld(t)

	mk := func(strength int) config.CreatureTemplate {
		return config.CreatureTemplate{
			HP: 30, MoveInterval: 3, AttackRange: 1, AttackDamage: 8, AttackCooldown: 30,
			BodyRadius: 0.4, BodyHeight: 1.0, ThrowStrength: strength,
		}
	}
	withThrow := map[components.CreatureKind]config.CreatureTemplate{
		components.CreatureWolf: mk(45),
	}
	withoutThrow := map[components.CreatureKind]config.CreatureTemplate{
		components.CreatureWolf: mk(0),
	}

	seedCreatures(wa.sim, []worldmap.CreatureSeed{{Kind: "wolf", X: 3, Y: 3}}, withThrow, 0.05)
	seedCreatures(wa.sim, []worldmap.CreatureSeed{{Kind: "wolf", X: 5, Y: 3}}, withoutThrow, 0.05)

	var strengths []int
	ecs.Query2[components.Creature, interactive.Thrower](wa.sim,
		func(_ ecs.Entity, _ *components.Creature, thr *interactive.Thrower) {
			strengths = append(strengths, thr.Strength)
		})
	if len(strengths) != 1 || strengths[0] != 45 {
		t.Fatalf("只有配了 throw_strength 的生物才该有投掷能力，实际 %v", strengths)
	}
}
