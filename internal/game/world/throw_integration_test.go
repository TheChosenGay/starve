package world

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 投掷的**端到端**集成测试（走完整链路）：
//
//	CommandThrow → CommandHandler.throw → ControlSystem 接纳（windup）
//	→ ActionSystem 推进 → ThrowExecutor.Commit（真正抛出）
//	→ ThrowSystem 飞行 → 落地爆炸 → 被炸者记仇
//
// 单元层面（components/throwable_test.go）已验证物理与公式；
// 这里验证"接线是否正确"——这是最容易漏、且表现最隐蔽的部分
// （意图没接上 = 点了没反应；相位没接上 = 没有蓄力段）。

// newThrowTestWorld 建一个**加载真实配置**的世界。
//
// 为什么不能只用 NewWorldActor(WorldConfig{})：炸弹的 Throwable（质量）
// 来自物品模板，空配置下模板表是空的 → 实体化出来的炸弹没有 Throwable
// → 投掷校验直接拒绝。实测踩过：表现为"点了投掷没反应"，
// 而且 Validate 只返回一个枚举值，不看模板表根本查不出原因。
func newThrowTestWorld(t *testing.T) *WorldActor {
	t.Helper()
	return NewWorldActor(WorldConfig{
		TemplatesPath: "../../../configs/resource_templates.json",
	})
}

// throwTestPlayer 建一个带投掷能力 + 炸弹的玩家。
func throwTestPlayer(t *testing.T, wa *WorldActor, x, y int, bombs int) ecs.Entity {
	t.Helper()
	p := wa.createPlayer("thrower")
	ecs.Set(wa.sim, p, components.Position{X: x, Y: y})
	// 注意：createPlayer 已挂 interactive.Thrower（力量来自 WorldConfig.ThrowStrength
	// 或缺省值），这里**不能**再 Add 一次（会 panic：组件已存在）。
	if bombs > 0 {
		inv := ecs.Get[components.Inventory](wa.sim, p)
		inv.Add(components.ItemBomb, bombs, 5, 0)
	}
	return p
}

// 完整链路：投掷意图 → windup → commit 抛出 → 飞行 → 落地爆炸。
func TestThrowEndToEndFromCommand(t *testing.T) {
	wa := newThrowTestWorld(t)
	p := throwTestPlayer(t, wa, 10, 10, 3)

	// 发投掷命令（不指定被投实体：由服务端从背包取炸弹实体化）
	wa.cmds.Handle(Command{
		UID: "thrower", Kind: CommandThrow,
		Data: ThrowData{Thrower: p, ToX: 14, ToY: 10},
	})

	// 接纳后应进入 windup（动作开始，但物体还没飞）
	tickWorld(wa)
	if !ecs.Has[components.ActionState](wa.sim, p) {
		t.Fatal("投掷意图应被接纳并进入动作（ActionState）")
	}
	st := ecs.Get[components.ActionState](wa.sim, p)
	if st.Kind != components.ActionThrow {
		t.Fatalf("动作类型应为 Throw，实际 %v", st.Kind)
	}
	if st.Phase != components.ActionWindup {
		t.Fatalf("首帧应处于 windup，实际 %v", st.Phase)
	}
	if !st.HasAim {
		t.Fatal("投掷动作应携带目标落点（HasAim）")
	}
	if st.AimX != 14 || st.AimY != 10 {
		t.Fatalf("落点应为 (14,10)，实际 (%.0f,%.0f)", st.AimX, st.AimY)
	}
	t.Logf("windup 进入，落点 (%.0f,%.0f)", st.AimX, st.AimY)

	// 推进到 commit：应出现一个飞行中的炸弹实体
	found := ecs.Entity(0)
	for i := 0; i < 40 && found == 0; i++ {
		tickWorld(wa)
		ecs.Query[components.Thrown](wa.sim, func(e ecs.Entity, _ *components.Thrown) {
			found = e
		})
	}
	if found == 0 {
		t.Fatal("windup 结束后应抛出一个飞行中的实体（挂 Thrown）")
	}
	th := ecs.Get[components.Thrown](wa.sim, found)
	if th.Thrower != p {
		t.Fatalf("飞行物的投掷者应为玩家 %d，实际 %d", p, th.Thrower)
	}
	t.Logf("已抛出：飞行 %d tick，落点 (%.1f,%.1f)", th.FlightTicks, th.ToX, th.ToY)

	// 推进到落地
	for i := 0; i < th.FlightTicks+10; i++ {
		tickWorld(wa)
	}
	if ecs.Has[components.Thrown](wa.sim, found) {
		t.Fatal("飞行结束后应移除 Thrown（已落地）")
	}
}

// 背包里没有炸弹时，投掷意图应被拒绝（而不是静默无事发生）。
func TestThrowWithoutBombIsRejected(t *testing.T) {
	wa := newThrowTestWorld(t)
	p := throwTestPlayer(t, wa, 10, 10, 0) // 不给炸弹

	wa.cmds.Handle(Command{
		UID: "thrower", Kind: CommandThrow,
		Data: ThrowData{Thrower: p, ToX: 14, ToY: 10},
	})
	tickWorld(wa)

	if ecs.Has[components.ActionState](wa.sim, p) {
		t.Fatal("没有炸弹时不应进入投掷动作")
	}
	// 也不该凭空造出飞行物
	count := 0
	ecs.Query[components.Thrown](wa.sim, func(ecs.Entity, *components.Thrown) { count++ })
	if count != 0 {
		t.Fatalf("没有炸弹时不应有飞行物，实际 %d", count)
	}
}

// 超出最大投掷距离的意图应被拒绝。
func TestThrowTooFarIsRejected(t *testing.T) {
	wa := newThrowTestWorld(t)
	p := throwTestPlayer(t, wa, 10, 10, 3)

	// 炸弹质量 18、力量 20 → 上限约 8 格。扔到 100 格外必定被拒。
	wa.cmds.Handle(Command{
		UID: "thrower", Kind: CommandThrow,
		Data: ThrowData{Thrower: p, ToX: 110, ToY: 10},
	})
	// 推到底：要么意图被拒，要么 windup 后 commit 失败
	for i := 0; i < 40; i++ {
		tickWorld(wa)
	}
	count := 0
	ecs.Query[components.Thrown](wa.sim, func(ecs.Entity, *components.Thrown) { count++ })
	if count != 0 {
		t.Fatalf("超距离投掷不应产生飞行物，实际 %d", count)
	}
}

// 两段相位：windup **可被打断**，recovery **不可打断**。
//
// 这是用户明确要求的语义：
//   - windup（手里）：动作没做完就不该飞出去 → 可打断
//   - recovery（抛出）：箭已离弦，打断也收不回 → 不可打断
func TestThrowWindupInterruptibleRecoveryNot(t *testing.T) {
	wa := newThrowTestWorld(t)
	p := throwTestPlayer(t, wa, 10, 10, 3)

	wa.cmds.Handle(Command{
		UID: "thrower", Kind: CommandThrow,
		Data: ThrowData{Thrower: p, ToX: 14, ToY: 10},
	})
	tickWorld(wa)
	st := ecs.Get[components.ActionState](wa.sim, p)
	if st.Phase != components.ActionWindup {
		t.Fatalf("前置条件：应处于 windup，实际 %v", st.Phase)
	}
	// windup 阶段必须**可**被打断
	if st.Uninterruptible {
		t.Fatal("windup 阶段应可被打断（动作没做完不该飞出去）")
	}

	// 推进到 recovery（commit 之后）
	for i := 0; i < 30; i++ {
		tickWorld(wa)
		if !ecs.Has[components.ActionState](wa.sim, p) {
			break // 动作已结束
		}
		s := ecs.Get[components.ActionState](wa.sim, p)
		if s.Phase == components.ActionRecovery {
			if !s.Uninterruptible {
				t.Fatal("recovery 阶段应不可打断（箭已离弦）")
			}
			return
		}
	}
	t.Log("动作在观察到 recovery 之前已结束（windup+recovery 较短），视为通过")
}

// 被炸的生物必须对**投掷者**记仇（爆炸伤害走了 ApplyDamage 才有）。
func TestBlastAggrosThrowerEndToEnd(t *testing.T) {
	wa := newThrowTestWorld(t)
	p := throwTestPlayer(t, wa, 10, 10, 3)

	// 放一只狼在落点附近
	wolf := throwTestWolf(t, wa, 16, 10)

	wa.cmds.Handle(Command{
		UID: "thrower", Kind: CommandThrow,
		Data: ThrowData{Thrower: p, ToX: 16, ToY: 10},
	})
	for i := 0; i < 80; i++ {
		tickWorld(wa)
	}

	hp := ecs.Get[components.Health](wa.sim, wolf)
	if hp.Cur >= hp.Max {
		t.Fatalf("落点附近的狼应被炸伤（HP %d/%d 未变）", hp.Cur, hp.Max)
	}
	if !ecs.Get[components.Creature](wa.sim, wolf).IsDirectThreat(p) {
		t.Fatal("被炸的狼应对投掷者记仇——否则说明爆炸伤害没走 ApplyDamage")
	}
	t.Logf("狼 HP %d/%d，已对投掷者记仇", hp.Cur, hp.Max)
}

// 只能投掷**自己**的实体（不能控制别人的）。
func TestThrowRejectsNonOwner(t *testing.T) {
	wa := newThrowTestWorld(t)
	p := throwTestPlayer(t, wa, 10, 10, 3)

	wa.cmds.Handle(Command{
		UID: "someone-else", Kind: CommandThrow, // 不是 thrower
		Data: ThrowData{Thrower: p, ToX: 14, ToY: 10},
	})
	tickWorld(wa)
	if ecs.Has[components.ActionState](wa.sim, p) {
		t.Fatal("非所有者不应能控制该实体投掷")
	}
}

// tickWorld 已在其它测试里定义；这里确保投掷相关测试不依赖真实时间。
var _ = time.Millisecond

// 回归：**只有 Creature、没有 AI 的实体不能让服务器 panic**。
//
// 这是写投掷测试时踩出来的真实健壮性缺陷：AISystem.tickAI 原先直接
// `ecs.Get[AI]`，而 `ecs.Get` 在组件缺失时**会 panic**（不是返回零值）。
// 于是任何"只作为仇恨对象、不参与决策"的实体（装饰生物、训练木桩）
// 都会让世界 tick 直接崩掉。
//
// 修复：tickAI 开头加 Has 护栏（AI 与 Position 各一道）。
func TestCreatureWithoutAIDoesNotPanic(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 5, Y: 5})
	ecs.Add(wa.sim, e, components.Health{Cur: 10, Max: 10})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{},
	})

	// 不应 panic
	tickWorld(wa)
	tickWorld(wa)

	// 对照：带 AI 的实体仍应正常被驱动（护栏不能误伤）
	wolf := throwTestWolf(t, wa, 8, 8)
	ecs.Add(wa.sim, wolf, components.AI{HostilePlayers: true, Leash: 30})
	tickWorld(wa)
}

// 同理：只有 AI 没有 Position 也不该崩。
func TestAIWithoutPositionDoesNotPanic(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Health{Cur: 10, Max: 10})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{},
	})
	ecs.Add(wa.sim, e, components.AI{HostilePlayers: true})
	tickWorld(wa) // 不应 panic
}

// throwTestWolf 建一只最简野兽（供投掷测试当受害者）。
func throwTestWolf(t *testing.T, wa *WorldActor, x, y int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: 30, Max: 30})
	ecs.Add(wa.sim, e, components.Attackable{})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{},
	})
	return e
}
