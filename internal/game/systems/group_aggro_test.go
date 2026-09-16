package systems

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 群体仇恨的**端到端**契约：打一只狼，同群的狼也应当把攻击者当成目标。
//
// 单元层面（components 包）已验证"仇恨值会传播"；这里验证传播**真的驱动决策**：
// AI.Target 被设上、AI.State 投影为追击/攻击，也就是玩家能观察到的"狼群一起扑上来"。
func newPackWorld(t *testing.T) *ecs.World {
	t.Helper()
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)
	// AISystem.worldPhase 依赖 DayCycle 资源；必须显式注入，否则 panic。
	w.AddResource(&components.DayCycle{Phase: 100})
	return w
}

// addWolf 生成一只狼：Creature + Position + AI + AOI + Attackable + Health + BehaviorTree。
func addWolf(w *ecs.World, x, y, radius int, visible []ecs.Entity) ecs.Entity {
	e := w.CreateEntity()
	ecs.Add(w, e, components.Creature{
		Kind:    components.CreatureWolf,
		Threats: map[ecs.Entity]int32{},
		HomeX:   x, HomeY: y, RoamRadius: 8,
	})
	ecs.Add(w, e, components.Position{X: x, Y: y})
	ecs.Add(w, e, components.AI{HostilePlayers: true, Leash: 30})
	ecs.Add(w, e, components.AOI{Radius: radius, Visible: visible})
	ecs.Add(w, e, components.Health{Max: 30, Cur: 30})
	ecs.Add(w, e, components.Attackable{})
	// 狼是掠食者：必须有 Weapon，否则 AttackDamage()==0，
	// projectedState 会把"有目标"当成被动生物的逃跑（与真实生成路径一致）。
	ecs.Add(w, e, components.Weapon{AttackRange: 1, AttackDamage: 8, AttackCooldown: 30})
	EnsureBehaviorTree(w, e, components.TreeKindPredator)
	return e
}

// 打一只狼 → 同群其它狼的 AI.Target 也指向攻击者。
func TestGroupAggroPackAcquiresTarget(t *testing.T) {
	w := newPackWorld(t)
	// 玩家（攻击者）
	player := w.CreateEntity()
	ecs.Add(w, player, components.Player{})
	ecs.Add(w, player, components.Position{X: 10, Y: 10})
	ecs.Add(w, player, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, player, components.Attackable{})

	victim := addWolf(w, 12, 10, 6, nil)
	allyA := addWolf(w, 13, 10, 6, nil)
	allyB := addWolf(w, 12, 11, 6, nil)
	// 受害者看得见同伴，且看得见玩家
	ecs.Get[components.AOI](w, victim).Visible = []ecs.Entity{player, allyA, allyB}

	// 打受害者：ApplyDamage 是唯一伤害入口（会写仇恨并传播）
	components.Attackable{}.ApplyDamage(w, victim, player, 8)

	if got := ecs.Get[components.Creature](w, victim).ThreatOf(player); got != 8 {
		t.Fatalf("受害者应获得完整仇恨 8，实际 %d", got)
	}
	for name, ally := range map[string]ecs.Entity{"A": allyA, "B": allyB} {
		if got := ecs.Get[components.Creature](w, ally).ThreatOf(player); got <= 0 {
			t.Fatalf("同伴 %s 应通过群体仇恨获得仇恨，实际 %d", name, got)
		}
	}

	// 跑一 tick AI：同伴应把玩家选为目标（决策真正被驱动）
	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)

	for name, ally := range map[string]ecs.Entity{"A": allyA, "B": allyB} {
		target := ecs.Get[components.AI](w, ally).Target
		if target != player {
			t.Fatalf("同伴 %s 的目标应为攻击者 %d，实际 %d（群体仇恨未驱动决策）",
				name, player, target)
		}
		state := ecs.Get[components.AI](w, ally).State
		if state != components.CreatureChase && state != components.CreatureAttack {
			t.Fatalf("同伴 %s 应进入追击/攻击状态，实际 %v", name, state)
		}
	}
}

// 异类旁观者不应因狼被打而锁定玩家（同类过滤的端到端验证）。
func TestGroupAggroDoesNotRecruitOtherSpecies(t *testing.T) {
	w := newPackWorld(t)
	player := w.CreateEntity()
	ecs.Add(w, player, components.Player{})
	ecs.Add(w, player, components.Position{X: 10, Y: 10})
	ecs.Add(w, player, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, player, components.Attackable{})

	victim := addWolf(w, 12, 10, 6, nil)
	// 一只野猪在旁边看着
	boar := w.CreateEntity()
	ecs.Add(w, boar, components.Creature{
		Kind: components.CreatureBoar, Threats: map[ecs.Entity]int32{},
		HomeX: 12, HomeY: 11, RoamRadius: 8,
	})
	ecs.Add(w, boar, components.Position{X: 12, Y: 11})
	ecs.Add(w, boar, components.AI{HostilePlayers: true, Leash: 22})
	ecs.Add(w, boar, components.AOI{Radius: 7, Visible: []ecs.Entity{player}})
	ecs.Add(w, boar, components.Health{Max: 50, Cur: 50})
	ecs.Add(w, boar, components.Attackable{})
	ecs.Add(w, boar, components.Weapon{AttackRange: 1, AttackDamage: 12, AttackCooldown: 35})
	EnsureBehaviorTree(w, boar, components.TreeKindPredator)

	ecs.Get[components.AOI](w, victim).Visible = []ecs.Entity{player, boar}

	components.Attackable{}.ApplyDamage(w, victim, player, 8)

	if got := ecs.Get[components.Creature](w, boar).ThreatOf(player); got != 0 {
		t.Fatalf("异类不应因狼被打而记仇，实际 %d", got)
	}
}

// 只传播一轮：第三只狼不应被"二手"唤醒（防连锁引爆全图）。
//
// 注意构造：far 必须在 near 的分摊范围内（距离 < 半径），否则二次传播会因
// 分摊为 0 而无法与"不传播"区分——那样测试就是假绿的（components 层踩过）。
func TestGroupAggroDoesNotChainAcrossPack(t *testing.T) {
	w := newPackWorld(t)
	player := w.CreateEntity()
	ecs.Add(w, player, components.Player{})
	ecs.Add(w, player, components.Position{X: 10, Y: 10})
	ecs.Add(w, player, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, player, components.Attackable{})

	victim := addWolf(w, 12, 10, 6, nil)
	near := addWolf(w, 14, 10, 6, nil)
	// far 紧挨 near（距离 1 < 半径 6）：发生二次传播时必然获得仇恨
	far := addWolf(w, 15, 10, 6, nil)

	ecs.Get[components.AOI](w, victim).Visible = []ecs.Entity{player, near}
	ecs.Get[components.AOI](w, near).Visible = []ecs.Entity{far}

	components.Attackable{}.ApplyDamage(w, victim, player, 8)

	if got := ecs.Get[components.Creature](w, near).ThreatOf(player); got <= 0 {
		t.Fatalf("直接同伴应获得仇恨，实际 %d", got)
	}
	if got := ecs.Get[components.Creature](w, far).ThreatOf(player); got != 0 {
		t.Fatalf("不应二次传播到更远的狼（会连锁引爆全图），实际 %d", got)
	}
}
