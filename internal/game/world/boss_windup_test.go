package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// Boss 技能的**表现/复制接口**测试。
//
// 背景：技能效果（炸弹 / 位移 / AOE）本来是"发生即结束"，客户端只能靠副作用
// 知道"它刚放了个大招"，模型因此没法播技能动画。现在的契约是：
// **每个技能起手都会起一个带 windup 的权威动作**（proto ACTION_KIND_BOSS_*），
// 客户端从 ActionState 就能拿到"正在放哪个技能、还要多久"，
// 且 slam/roar 的动作时长 == 决策节点的前摇 tick（动作结束 == 效果发生）。
//
// 这里走真实 AI：给 Boss 挂 boss 行为树 + 仇恨，跑系统，观察它自己放技能。

// bossWindupSubject 造一个"挂了 Boss 行为树的怪 + 一个被仇恨的玩家"。
func bossWindupSubject(t *testing.T, wa *WorldActor, hp int) (ecs.Entity, ecs.Entity) {
	t.Helper()
	boss := spawnBTCreature(t, wa, 10, 10, components.TreeKindBoss)
	// 投弹要投掷能力（否则炸弹扔不出去；表现动作不受影响，但我们要测真实链路）
	ecs.Add(wa.sim, boss, interactive.Thrower{Strength: 40})
	ecs.Set(wa.sim, boss, components.Health{Cur: hp, Max: 400})

	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 10, Y: 8})
	// 正式玩法由 AOI 感知 + 受击建立仇恨；测试直接塞进去（与 cmd/bossdemo 同法），
	// 免得依赖感知半径/可见性这些与本次契约无关的细节。
	ecs.Get[components.Creature](wa.sim, boss).Threats[player] = 50
	return boss, player
}

// 阶段一：投弹起手必须出现在权威动作里（客户端据此播投弹动画）。
func TestBossThrowAnnouncesWindupAction(t *testing.T) {
	wa := newThrowTestWorld(t)
	boss, _ := bossWindupSubject(t, wa, 400) // 血高 = 阶段一（只会投弹）

	var windup int64 = -1
	for i := 0; i < 60 && windup < 0; i++ {
		tickWorld(wa)
		ecs.Query[components.ActionState](wa.sim, func(e ecs.Entity, st *components.ActionState) {
			if e != boss || st.Kind != components.ActionBossThrow || windup >= 0 {
				return
			}
			windup = st.CommitTick - st.PhaseStartTick
			if st.Phase != components.ActionWindup {
				t.Fatalf("技能动作应从前摇开始，实际 phase=%v", st.Phase)
			}
		})
	}
	if windup <= 0 {
		t.Fatalf("阶段一投弹必须起一个带前摇的权威动作（客户端据此播动画）；"+
			"观察到 windup=%d —— 没有它，模型只能等爆炸才知道 Boss 放了大招", windup)
	}

	// 前摇结束、动作收招后必须释放（否则会永久占住动作槽，Boss 再也放不出别的技能）
	for i := 0; i < int(windup)+5; i++ {
		tickWorld(wa)
	}
	if ecs.Has[components.ActionState](wa.sim, boss) {
		t.Fatal("投弹动作应在 windup 结束后完成并移除（recovery=0，后摇由决策节点自己计时）")
	}
}

// 阶段二：嚎叫与锤地同样必须出现在权威动作里。
func TestBossPhaseTwoAnnouncesRoarAndSlam(t *testing.T) {
	wa := newThrowTestWorld(t)
	boss, _ := bossWindupSubject(t, wa, 100) // 血低 = 阶段二（嚎叫 + 闪现 + 三拳一砸）

	seen := map[components.ActionKind]bool{}
	for i := 0; i < 400; i++ {
		tickWorld(wa)
		ecs.Query[components.ActionState](wa.sim, func(e ecs.Entity, st *components.ActionState) {
			if e == boss {
				seen[st.Kind] = true
			}
		})
	}
	if !seen[components.ActionBossRoar] {
		t.Fatalf("阶段二的嚎叫必须在权威动作里可见（否则模型不知道何时播嚎叫）；实际 %v", seen)
	}
	if !seen[components.ActionBossSlam] {
		t.Fatalf("锤地必须在权威动作里可见（否则 AOE 只有爆炸表现、没有起手动画）；实际 %v", seen)
	}
}
