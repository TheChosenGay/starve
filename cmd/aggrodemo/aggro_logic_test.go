package main

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 演示逻辑的契约测试。跑在宿主机（不需要 WASM），
// 验证"打一只狼 → 同类获得仇恨 → 一起扑上来"这条链真的成立。
//
// 这些测试的价值：如果群体仇恨被改坏，aggro.html 会显示"打了没反应"，
// 但那是浏览器里肉眼看不出的静默失败——这里先钉住。

func TestDemoWorldBuilds(t *testing.T) {
	w := newAggroWorld(6)
	if len(w.wolves) != 6 {
		t.Fatalf("应有 6 只狼，实际 %d", len(w.wolves))
	}
	snap := w.snapshot()
	if snap.TotalWolves != 6 {
		t.Fatalf("快照狼数应为 6，实际 %d", snap.TotalWolves)
	}
	if snap.AggroCount != 0 {
		t.Fatalf("初始不应有狼锁定玩家，实际 %d", snap.AggroCount)
	}
	// 快照切片必须非 nil（nil 会被 JSON 成 null，前端 for...of 直接白屏）
	if snap.Wolves == nil || snap.Events == nil {
		t.Fatal("快照切片不能为 nil（会序列化成 null 导致前端白屏）")
	}
}

// 核心演示契约：攻击一只狼 → 感知范围内的同类都获得仇恨并锁定玩家。
func TestAttackingOneWolfAggrosThePack(t *testing.T) {
	w := newAggroWorld(6)

	// 全部狼初始仇恨为 0
	for i, e := range w.wolves {
		if got := ecs.Get[components.Creature](w.sim, e).ThreatOf(w.player); got != 0 {
			t.Fatalf("狼#%d 初始仇恨应为 0，实际 %d", i, got)
		}
	}

	if !w.attackWolf(0) {
		t.Fatal("攻击第 0 只狼应成功")
	}

	// 被攻击的那只：完整伤害 + DirectThreat
	victim := w.wolves[0]
	vc := ecs.Get[components.Creature](w.sim, victim)
	if got := vc.DirectTarget(); got != w.player {
		t.Fatalf("被攻击的狼应把玩家设为**直接仇恨**，实际 %d", got)
	}

	// 其余同类：获得传播来的仇恨（但不进 DirectThreat）
	spread := 0
	for i, e := range w.wolves {
		if i == 0 {
			continue
		}
		c := ecs.Get[components.Creature](w.sim, e)
		if _, ok := c.Indirect[w.player]; ok {
			spread++
		}
		if c.IsDirectThreat(w.player) {
			t.Fatalf("狼#%d 只是被通知，不应是直接仇恨", i)
		}
	}
	if spread == 0 {
		t.Fatal("攻击一只狼后，同类应通过群体仇恨获得仇恨（这是演示的核心）")
	}
	t.Logf("传播结果：%d/%d 只同类获得仇恨", spread, len(w.wolves)-1)
}

// 跑若干 tick 后，狼群应真的把玩家当目标（决策被驱动，而不只是仇恨表有数字）。
func TestPackConvergesOnPlayerAfterTicks(t *testing.T) {
	w := newAggroWorld(6)
	w.attackWolf(0)

	// 推进足够多 tick 让 AI 结算目标
	for i := 0; i < 10; i++ {
		w.step()
	}

	snap := w.snapshot()
	if snap.AggroCount < 2 {
		t.Fatalf("打一只狼后应有多只狼锁定玩家，实际 %d（传播未驱动决策）", snap.AggroCount)
	}
	t.Logf("锁定玩家的狼：%d/%d", snap.AggroCount, snap.TotalWolves)
}

// 规则 ②：间接仇恨存的是"**我**到目标的距离"，多个来源时按它选最近的。
//
// 注意与旧模型的区别：旧版存的是"受害者到攻击者的分摊数值"，
// 新版存的是接收方自己到目标的距离（随位置随时重算）。
func TestIndirectThreatRecordsOwnDistance(t *testing.T) {
	w := newAggroWorld(6)
	w.attackWolf(0)
	// 距离由 AISystem 每 tick 按各自位置重算；刚传播完还是占位 0。
	w.step()

	// 所有狼都在传播范围内 → 都应拿到间接仇恨，且距离=各自到玩家的距离
	got := 0
	for i, e := range w.wolves {
		if i == 0 {
			continue // 被攻击的那只是直接仇恨
		}
		c := ecs.Get[components.Creature](w.sim, e)
		d, ok := c.Indirect[w.player]
		if !ok {
			continue
		}
		got++
		// 距离必须等于"该狼到玩家"的切比雪夫距离（不是受害者到玩家的）
		pos := ecs.Get[components.Position](w.sim, e)
		plPos := ecs.Get[components.Position](w.sim, w.player)
		want := int(chebyshev(pos.X, pos.Y, plPos.X, plPos.Y))
		if d != want {
			t.Fatalf("狼#%d 的间接仇恨距离应为自身到玩家的 %d，实际 %d", i, want, d)
		}
	}
	if got == 0 {
		t.Fatal("仇恨传播半径内的同类都应获得间接仇恨")
	}
	t.Logf("%d/%d 只同类获得间接仇恨（传播半径 %d）", got, len(w.wolves)-1, demoAoiRadius)
}

// 超出感知范围的同类不应被传播（把狼群拉得很开）。
func TestFarWolvesNotNotified(t *testing.T) {
	w := newAggroWorld(6)
	// 把第 5 只狼挪到很远的地方（超出 demoAoiRadius）
	far := w.wolves[5]
	p := ecs.Get[components.Position](w.sim, far)
	p.X = int(demoOrigin) + 30
	p.Y = int(demoOrigin)

	w.attackWolf(0)
	if got := ecs.Get[components.Creature](w.sim, far).ThreatOf(w.player); got != 0 {
		t.Fatalf("感知范围外的同类不应获得仇恨，实际 %d", got)
	}
}

// reset 应把世界恢复到可重复演示的初始状态。
func TestResetRestoresInitialState(t *testing.T) {
	w := newAggroWorld(4)
	w.attackWolf(0)
	for i := 0; i < 20; i++ {
		w.step()
	}
	if w.snapshot().AggroCount == 0 {
		t.Fatal("前置条件：攻击后应有狼锁定玩家")
	}

	w.reset(4)
	snap := w.snapshot()
	if snap.AggroCount != 0 {
		t.Fatalf("重置后不应有狼锁定玩家，实际 %d", snap.AggroCount)
	}
	if snap.Player.HP != demoPlayerHP {
		t.Fatalf("重置后玩家应回满血，实际 %d", snap.Player.HP)
	}
	for i, e := range w.wolves {
		if got := ecs.Get[components.Creature](w.sim, e).ThreatOf(w.player); got != 0 {
			t.Fatalf("重置后狼#%d 仇恨应为 0，实际 %d", i, got)
		}
	}
}

// setWolfCount 应能重建狼群（前端滑块）。
func TestSetWolfCountRebuildsPack(t *testing.T) {
	w := newAggroWorld(4)
	w.setWolfCount(10)
	if len(w.wolves) != 10 {
		t.Fatalf("应重建为 10 只狼，实际 %d", len(w.wolves))
	}
	if got := w.snapshot().TotalWolves; got != 10 {
		t.Fatalf("快照应报告 10 只，实际 %d", got)
	}
}

// 演示里有只狼死亡不应 panic（玩家多打几下就会死）。
//
// 注意：ApplyDamage 只扣血，**不直接挂 Dead 组件**——死亡由 DeathSystem 结算。
// 所以必须一边打一边推进 tick，否则血扣到 0 也不会变成"已死亡"。
func TestKillingWolfIsSafe(t *testing.T) {
	w := newAggroWorld(4)
	// 玩家伤害 12、狼 HP 30 → 3 次即可打死；穿插 step 让死亡结算生效
	for i := 0; i < 20; i++ {
		w.attackWolf(0)
		w.step()
	}
	hp := ecs.Get[components.Health](w.sim, w.wolves[0])
	if !ecs.Has[components.Dead](w.sim, w.wolves[0]) {
		t.Fatalf("打了 20 次后狼应已死亡（HP=%d、Dead=%v）",
			hp.Cur, ecs.Has[components.Dead](w.sim, w.wolves[0]))
	}
	// 关键：快照仍能正常生成，且死亡狼标记为 Alive=false
	snap := w.snapshot()
	if snap.Wolves[0].Alive {
		t.Fatal("已死亡的狼应标记 Alive=false")
	}
	// 再打已死的狼应安全返回 false
	if w.attackWolf(0) {
		t.Fatal("攻击已死亡的狼应返回 false")
	}
}
