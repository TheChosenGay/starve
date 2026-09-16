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
	// AISystem 依赖两个资源，测试里必须显式注入，否则 panic：
	//   - DayCycle：worldPhase() 读世界时钟
	//   - ControlQueue：行为树攻击动作经 EnqueueControl 下发控制意图
	w.AddResource(&components.DayCycle{Phase: 100})
	w.AddResource(&ControlQueue{})
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

// 回归：群体仇恨**不能被衰减吃光**。
//
// 这是 WASM 演示（aggro.html）暴露出来的真实问题：
// 仇恨原先每 tick -1（20/秒），而群体仇恨是按伤害**分摊**的——近处同伴通常
// 只拿到 5~8 点。于是玩家只打一下时，同伴仅锁定 4 tick（0.2 秒）就回去游荡，
// "狼群一起扑上来"退化成一次几乎看不见的闪烁。
//
// 修复：衰减改为每 ThreatDecayTicks 个 tick 减 1（配置项，狼缺省 10 = 0.5 秒减 1）。
// 实测同伴锁定时间 4 tick → 47 tick（0.2s → 2.4s），足够从近处赶到加入战斗。
func TestPropagatedThreatSurvivesLongEnoughToEngage(t *testing.T) {
	run := func(decayTicks int) int {
		w := newPackWorld(t)
		player := w.CreateEntity()
		ecs.Add(w, player, components.Player{})
		ecs.Add(w, player, components.Position{X: 10, Y: 10})
		ecs.Add(w, player, components.Health{Max: 100, Cur: 100})
		ecs.Add(w, player, components.Attackable{})

		victim := addWolf(w, 12, 10, 6, nil)
		ally := addWolf(w, 14, 10, 6, nil)
		ecs.Get[components.AOI](w, victim).Visible = []ecs.Entity{player, ally}
		ecs.Get[components.AI](w, ally).ThreatDecayTicks = decayTicks

		// 只打一下：不持续攻击
		components.Attackable{}.ApplyDamage(w, victim, player, 8)

		ai := &AISystem{}
		locked := 0
		for tick := 0; tick < 200; tick++ {
			ai.Update(w, 50*time.Millisecond)
			ecs.Resource[components.DayCycle](w).Phase++
			if ecs.Get[components.AI](w, ally).Target == player {
				locked++
			}
		}
		return locked
	}

	slow := run(10) // 修复后的缺省（0.5 秒减 1）
	fast := run(1)  // 旧行为（每 tick 减 1）

	t.Logf("每 tick 衰减: 锁定 %d tick (%.1fs) | 每 10 tick 衰减: 锁定 %d tick (%.1fs)",
		fast, float64(fast)*0.05, slow, float64(slow)*0.05)

	// 旧行为：几乎立刻脱战（这正是要防的退化）
	if fast > 10 {
		t.Fatalf("前置条件：每 tick 衰减时同伴应很快脱战，实际锁定 %d tick", fast)
	}
	// 修复后：至少维持 1.5 秒，够同伴跑到玩家面前
	if slow < 30 {
		t.Fatalf("单次攻击后同伴至少应锁定 30 tick(1.5s)，实际 %d tick —— "+
			"群体仇恨又退化成闪烁了", slow)
	}
	if slow <= fast*3 {
		t.Fatalf("按间隔衰减应显著延长锁定时间：fast=%d slow=%d", fast, slow)
	}
}
