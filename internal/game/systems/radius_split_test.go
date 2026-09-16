package systems

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 感知半径与仇恨传播半径**分开**后的两条契约（缺一不可）：
//
//  1. 感知范围**不因**仇恨传播半径变大而变大 —— 否则狼隔着 16 格就发现玩家，
//     潜行完全失效（这正是把两者合一时必然付出的代价）。
//  2. 仇恨传播范围确实更大 —— 否则群体仇恨又只有身边一两只响应。
//
// 实现上只查**一份** AOI（半径取两者较大者，省一半方格标记成本），
// 感知则对 Visible 再做一次精确距离复核（见 components.AOI.InPerception）。
func TestPerceptionAndThreatRadiusAreIndependent(t *testing.T) {
	w := newPackWorld(t)

	// 狼：感知 6、仇恨传播 16
	mk := func(x, y int) ecs.Entity {
		e := w.CreateEntity()
		ecs.Add(w, e, components.Position{X: x, Y: y})
		ecs.Add(w, e, components.Health{Cur: 30, Max: 30})
		ecs.Add(w, e, components.Attackable{})
		ecs.Add(w, e, components.Weapon{AttackRange: 1, AttackDamage: 8, AttackCooldown: 30})
		ecs.Add(w, e, components.Creature{Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{}})
		ecs.Add(w, e, components.AOI{Radius: 16, Perception: 6, Threat: 16})
		ecs.Add(w, e, components.AI{HostilePlayers: true, Leash: 30})
		EnsureBehaviorTree(w, e, components.TreeKindPredator)
		return e
	}
	// 玩家放在 12 格外：在仇恨传播范围内(16)、但**不在感知范围内**(6)
	player := w.CreateEntity()
	ecs.Add(w, player, components.Player{})
	ecs.Add(w, player, components.Position{X: 10, Y: 10})
	ecs.Add(w, player, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, player, components.Attackable{})

	wolf := mk(22, 10) // 距离 12

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)
	// 感知不到 → 不应主动锁定
	if got := ecs.Get[components.AI](w, wolf).Target; got != 0 {
		t.Fatalf("12 格外的玩家超出感知半径(6)，狼不该发现他，实际 target=%d", got)
	}
	if ecs.Get[components.Creature](w, wolf).IsDirectThreat(player) {
		t.Fatal("超出感知半径不该成为直接仇恨（潜行应有效）")
	}
	t.Log("✓ 12 格外：仇恨传播半径(16)内有记录，但感知(6)不触发 → 潜行有效")

	// 但同伴在这个范围内被打时，它**应该**收到通知
	buddy := addWolf(w, 20, 10, 6, nil)
	ecs.Get[components.AOI](w, buddy).Radius = 16
	ecs.Get[components.AOI](w, buddy).Perception = 6
	ecs.Get[components.AOI](w, buddy).Threat = 16
	// 让 wolf 能"听见"同伴（在 wolf 的传播范围内）
	ecs.Get[components.AOI](w, buddy).Visible = []ecs.Entity{player, wolf}
	components.Attackable{}.ApplyDamage(w, buddy, player, 8)

	if _, ok := ecs.Get[components.Creature](w, wolf).Indirect[player]; !ok {
		t.Fatal("同伴被打时，仇恨传播半径内的狼应收到间接仇恨")
	}
	t.Log("✓ 同伴被打：仇恨传播半径(16)内收到间接仇恨")
}
