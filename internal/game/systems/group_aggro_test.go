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

	if got := ecs.Get[components.Creature](w, victim).DirectTarget(); got != player {
		t.Fatalf("受害者应把玩家设为直接仇恨，实际 %d", got)
	}
	for name, ally := range map[string]ecs.Entity{"A": allyA, "B": allyB} {
		c := ecs.Get[components.Creature](w, ally)
		if _, ok := c.Indirect[player]; !ok {
			t.Fatalf("同伴 %s 应通过群体仇恨获得间接仇恨", name)
		}
		if c.IsDirectThreat(player) {
			t.Fatalf("同伴 %s 只是被通知，不应是直接仇恨", name)
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

	if got := len(ecs.Get[components.Creature](w, boar).Indirect); got != 0 {
		t.Fatalf("异类不应因狼被打而记仇，实际 %d 条", got)
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

	if _, ok := ecs.Get[components.Creature](w, near).Indirect[player]; !ok {
		t.Fatal("直接同伴应获得间接仇恨")
	}
	if got := len(ecs.Get[components.Creature](w, far).Indirect); got != 0 {
		t.Fatalf("不应二次传播到更远的狼（会连锁引爆全图），实际 %d 条", got)
	}
}

// 规则 ② / ③：间接仇恨**不靠数值衰减**，而是"在范围内就维持、跑出去就清除"。
//
// 为什么改成这样（用户的设计）：原先仇恨是每 tick -1 的数值，而群体仇恨按伤害
// **分摊**、近处同伴只拿到 5~8 点，于是 200~400ms 就忘光——实测单次攻击后同伴
// 只锁定 4 tick，"群体仇恨"退化成一次闪烁。新模型里间接仇恨由**距离**决定：
// 只要目标还在范围内就一直有效，仇恨值本身随距离变化（不是单调倒计时）。
func TestIndirectThreatPersistsWhileInRange(t *testing.T) {
	w := newPackWorld(t)
	player := w.CreateEntity()
	ecs.Add(w, player, components.Player{})
	ecs.Add(w, player, components.Position{X: 10, Y: 10})
	ecs.Add(w, player, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, player, components.Attackable{})

	victim := addWolf(w, 12, 10, 6, nil)
	ally := addWolf(w, 14, 10, 6, nil)
	ecs.Get[components.AOI](w, victim).Visible = []ecs.Entity{player, ally}
	// ally 看不见玩家（否则"看见即直接仇恨"会短路掉间接仇恨）
	ecs.Get[components.AOI](w, ally).Visible = nil

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
	t.Logf("同伴锁定玩家 %d/200 tick (%.1f 秒)", locked, float64(locked)*0.05)
	if locked < 150 {
		t.Fatalf("只要目标在范围内，间接仇恨就该一直有效（实测 %d/200 tick）——"+
			"若这里退化，说明又变回『数值倒计时』了", locked)
	}
}

// 规则 ③：目标跑出仇恨范围 → 清除（遗忘）。
func TestThreatClearedWhenTargetLeavesRange(t *testing.T) {
	w := newPackWorld(t)
	player := w.CreateEntity()
	ecs.Add(w, player, components.Player{})
	ecs.Add(w, player, components.Position{X: 10, Y: 10})
	ecs.Add(w, player, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, player, components.Attackable{})

	victim := addWolf(w, 12, 10, 6, nil)
	ally := addWolf(w, 14, 10, 6, nil)
	ecs.Get[components.AOI](w, victim).Visible = []ecs.Entity{player, ally}
	ecs.Get[components.AOI](w, ally).Visible = nil

	components.Attackable{}.ApplyDamage(w, victim, player, 8)

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)
	if ecs.Get[components.AI](w, ally).Target != player {
		t.Fatal("前置条件：同伴应已锁定玩家")
	}

	// 玩家跑到很远（超出拴绳 30）
	p := ecs.Get[components.Position](w, player)
	p.X, p.Y = 200, 200
	ai.Update(w, 50*time.Millisecond)

	c := ecs.Get[components.Creature](w, ally)
	if len(c.Indirect) != 0 {
		t.Fatalf("目标跑出范围后间接仇恨应清除，实际 %v", c.Indirect)
	}
	if got := ecs.Get[components.AI](w, ally).Target; got != 0 {
		t.Fatalf("跑出范围后不应再有目标，实际 %d", got)
	}
}

// 规则 ③：目标死亡 → 清除仇恨。
func TestThreatClearedWhenTargetDies(t *testing.T) {
	w := newPackWorld(t)
	player := w.CreateEntity()
	ecs.Add(w, player, components.Player{})
	ecs.Add(w, player, components.Position{X: 10, Y: 10})
	ecs.Add(w, player, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, player, components.Attackable{})

	victim := addWolf(w, 12, 10, 6, nil)
	ally := addWolf(w, 14, 10, 6, nil)
	ecs.Get[components.AOI](w, victim).Visible = []ecs.Entity{player, ally}
	ecs.Get[components.AOI](w, ally).Visible = nil

	components.Attackable{}.ApplyDamage(w, victim, player, 8)

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)

	// 玩家死亡
	ecs.Add(w, player, components.Dead{})
	ai.Update(w, 50*time.Millisecond)

	c := ecs.Get[components.Creature](w, ally)
	if len(c.Indirect) != 0 {
		t.Fatalf("目标死亡后间接仇恨应清除，实际 %v", c.Indirect)
	}
	if got := ecs.Get[components.AI](w, ally).Target; got != 0 {
		t.Fatalf("目标死亡后不应再有目标，实际 %d", got)
	}
}
