package systems

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 验证用户提出的核心问题：
//  1. 同时收到两处"同伴被打"的通知 → 怎么决定打谁？（是否按仇恨值）
//  2. "正在打我的攻击者" 优先级是否高于 "仇恨通知传来的攻击者"？
func TestAggroPrioritySelfAttackerOverPropagated(t *testing.T) {
	w := newPackWorld(t)

	// 两个玩家：甲攻击了同伴（远），乙正在攻击我（近）
	farPlayer := w.CreateEntity()
	ecs.Add(w, farPlayer, components.Player{})
	ecs.Add(w, farPlayer, components.Position{X: 10, Y: 10})
	ecs.Add(w, farPlayer, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, farPlayer, components.Attackable{})

	nearPlayer := w.CreateEntity()
	ecs.Add(w, nearPlayer, components.Player{})
	ecs.Add(w, nearPlayer, components.Position{X: 12, Y: 10})
	ecs.Add(w, nearPlayer, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, nearPlayer, components.Attackable{})

	// 同伴（与我要同群）
	buddy := addWolf(w, 11, 10, 6, nil)
	// 我自己
	me := addWolf(w, 12, 11, 6, nil)

	// 同伴看得见甲**和我**（群体仇恨只传给视野内的同类）；我看得见两人
	ecs.Get[components.AOI](w, buddy).Visible = []ecs.Entity{farPlayer, me}
	ecs.Get[components.AOI](w, me).Visible = []ecs.Entity{nearPlayer, farPlayer}

	// 甲打了同伴（触发群体仇恨传播）
	components.Attackable{}.ApplyDamage(w, buddy, farPlayer, 8)
	// 乙打了我自己（真实受击）
	components.Attackable{}.ApplyDamage(w, me, nearPlayer, 8)

	t.Logf("传播后 我的仇恨表: far=%d near=%d",
		ecs.Get[components.Creature](w, me).ThreatOf(farPlayer),
		ecs.Get[components.Creature](w, me).ThreatOf(nearPlayer))

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)

	got := ecs.Get[components.AI](w, me).Target
	t.Logf("选中的目标: %d (near=%d far=%d)", got, nearPlayer, farPlayer)
	if got != nearPlayer {
		t.Fatalf("正在攻击我的 %d 应优先于仇恨通知传来的 %d，实际选中 %d",
			nearPlayer, farPlayer, got)
	}
}

// 问题1：同时收到**两处**"同伴被打"的通知，怎么决定打谁？
//
// 结论：按**仇恨值**（仇恨值本身由传播时的伤害×距离分摊决定）。
// 注意：两处通知强度必须不同，否则无法区分"按仇恨值选"与"按遍历顺序选"。
func TestAggroTwoNotificationsPicksByThreat(t *testing.T) {
	w := newPackWorld(t)

	p1 := w.CreateEntity()
	ecs.Add(w, p1, components.Player{})
	ecs.Add(w, p1, components.Position{X: 10, Y: 10})
	ecs.Add(w, p1, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, p1, components.Attackable{})

	p2 := w.CreateEntity()
	ecs.Add(w, p2, components.Player{})
	ecs.Add(w, p2, components.Position{X: 13, Y: 10})
	ecs.Add(w, p2, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, p2, components.Attackable{})

	// 两处被攻击的同伴
	hurt1 := addWolf(w, 11, 10, 6, nil)
	hurt2 := addWolf(w, 11, 11, 6, nil)
	me := addWolf(w, 12, 11, 6, nil)

	// **关键**：两只同伴都必须看得见我，群体仇恨才会传到我这里。
	ecs.Get[components.AOI](w, hurt1).Visible = []ecs.Entity{p1, me}
	ecs.Get[components.AOI](w, hurt2).Visible = []ecs.Entity{p2, me}
	ecs.Get[components.AOI](w, me).Visible = []ecs.Entity{p1, p2}

	// p1 造成大伤害，p2 小伤害 → 两条通知强度不同
	components.Attackable{}.ApplyDamage(w, hurt1, p1, 20)
	components.Attackable{}.ApplyDamage(w, hurt2, p2, 3)

	myCreature := ecs.Get[components.Creature](w, me)
	t1, t2 := myCreature.ThreatOf(p1), myCreature.ThreatOf(p2)
	t.Logf("我的仇恨表: p1=%d p2=%d", t1, t2)
	if t1 <= t2 {
		t.Fatalf("传播强度应随伤害不同：p1(伤害20)=%d 应大于 p2(伤害3)=%d", t1, t2)
	}

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)
	got := ecs.Get[components.AI](w, me).Target
	t.Logf("选中: %d (p1=%d p2=%d)", got, p1, p2)
	if got != p1 {
		t.Fatalf("应按仇恨值选伤害更大的一方(p1=%d)，实际 %d", p1, got)
	}
}

// 对抗性验证（最强的一条）：让"通知来的目标"仇恨值**远超**"正在打我的人"，
// 确认优先级仍然正确。
//
// 这是用户提出的担忧的极端形式。实测数据见日志：
//
//	self(正在打我, 伤害1)        = 1
//	other(通知来, 伤害100×5只同伴) = 125
//
// 若只按仇恨值比大小，生物会抛下正在揍它的敌人、跑去打远处的那个（曾是真 bug）。
// 现在改为两级优先级（DirectThreat 优先），数值再大也不会被淹。
func TestAggroDirectAttackerWinsDespiteHugePropagatedThreat(t *testing.T) {
	w := newPackWorld(t)

	self := w.CreateEntity() // 正在打我（伤害很小）
	ecs.Add(w, self, components.Player{})
	ecs.Add(w, self, components.Position{X: 12, Y: 10})
	ecs.Add(w, self, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, self, components.Attackable{})

	other := w.CreateEntity() // 只打了同伴，但伤害巨大 + 5 只同伴一起传
	ecs.Add(w, other, components.Player{})
	ecs.Add(w, other, components.Position{X: 10, Y: 10})
	ecs.Add(w, other, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, other, components.Attackable{})

	me := addWolf(w, 12, 11, 6, nil)
	var buddies []ecs.Entity
	for i := 0; i < 5; i++ {
		b := addWolf(w, 11, 10+i%2, 6, nil)
		// 每只同伴都看得见 other 和我，仇恨才会传到我这里
		ecs.Get[components.AOI](w, b).Visible = []ecs.Entity{other, me}
		buddies = append(buddies, b)
	}
	ecs.Get[components.AOI](w, me).Visible = []ecs.Entity{self, other}

	for _, b := range buddies {
		components.Attackable{}.ApplyDamage(w, b, other, 100)
	}
	components.Attackable{}.ApplyDamage(w, me, self, 1)

	myCreature := ecs.Get[components.Creature](w, me)
	selfThreat := myCreature.ThreatOf(self)
	otherThreat := myCreature.ThreatOf(other)
	t.Logf("仇恨表: self(正在打我,伤害1)=%d other(通知,伤害100×5)=%d", selfThreat, otherThreat)
	if otherThreat <= selfThreat {
		t.Fatalf("本测试要构造'通知仇恨远大于自击者'的场景，实际 self=%d other=%d",
			selfThreat, otherThreat)
	}

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)
	got := ecs.Get[components.AI](w, me).Target
	t.Logf("选中: %d (self=%d other=%d)", got, self, other)
	if got != self {
		t.Fatalf("正在攻击我的 %d 必须优先（哪怕通知仇恨 %d > 我的 %d），实际选中 %d",
			self, otherThreat, selfThreat, got)
	}
}

// 目标死亡后 DirectThreat 里的残留应被清理（与 Threats 同步）。
func TestDirectThreatClearedWhenAttackerRemoved(t *testing.T) {
	w := newPackWorld(t)
	player := w.CreateEntity()
	ecs.Add(w, player, components.Position{X: 0, Y: 0})
	victim := addWolf(w, 10, 10, 6, nil)

	ecs.Get[components.Creature](w, victim).AddThreat(w, victim, player, 8)
	ecs.Get[components.Creature](w, victim).MarkDirectThreat(player)
	if !ecs.Get[components.Creature](w, victim).IsDirectThreat(player) {
		t.Fatal("前置条件：应已标记")
	}
	// 攻击者死亡
	ecs.Add(w, player, components.Dead{})

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)

	c := ecs.Get[components.Creature](w, victim)
	if c.IsDirectThreat(player) {
		t.Fatal("攻击者已死亡，DirectThreat 残留应被清理（否则会一直锁着无效目标）")
	}
	if c.ThreatOf(player) != 0 {
		t.Fatal("Threats 里的残留也应被清理")
	}
}

// 回归：**受击窗口过期之后**，优先级仍必须成立。
//
// 为什么这条测试不可省：ApplyDamage 会同时写 AI.LastHitBy，而 LastHitBy 有
// hit_memory_ticks 窗口（狼=10 tick=0.5s），但攻击冷却 attack_cooldown=30 tick=1.5s。
// 也就是说两次攻击之间 LastHitBy 必然过期——若只靠它做优先级判断，
// 生物会在"打我的人"与"通知来的人"之间来回摇摆。
//
// 因此必须推进世界时钟越过窗口，验证 **DirectThreat（长期记忆）** 独立生效。
// （曾踩过：测试紧接着 Update，LastHitBy 还在窗口内，于是删掉 DirectThreat
//
//	标记也能通过——测试是假绿的。）
func TestAggroPriorityHoldsAfterHitWindowExpires(t *testing.T) {
	w := newPackWorld(t)

	self := w.CreateEntity()
	ecs.Add(w, self, components.Player{})
	ecs.Add(w, self, components.Position{X: 12, Y: 10})
	ecs.Add(w, self, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, self, components.Attackable{})

	other := w.CreateEntity()
	ecs.Add(w, other, components.Player{})
	ecs.Add(w, other, components.Position{X: 10, Y: 10})
	ecs.Add(w, other, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, other, components.Attackable{})

	me := addWolf(w, 12, 11, 6, nil)
	ecs.Get[components.AOI](w, me).Visible = []ecs.Entity{self, other}

	// self 亲自打我；other 通过同伴通知获得远超的仇恨
	components.Attackable{}.ApplyDamage(w, me, self, 1)
	var buddies []ecs.Entity
	for i := 0; i < 5; i++ {
		b := addWolf(w, 11, 10+i%2, 6, nil)
		ecs.Get[components.AOI](w, b).Visible = []ecs.Entity{other, me}
		buddies = append(buddies, b)
	}
	for _, b := range buddies {
		components.Attackable{}.ApplyDamage(w, b, other, 100)
	}

	myAI := ecs.Get[components.AI](w, me)
	// 让受击窗口失效：把记录时刻推远（Phase 是单调递增的世界时钟）。
	myAI.HitMemoryTicks = 1
	ecs.Resource[components.DayCycle](w).Phase = 1000

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)

	got := myAI.Target
	selfThreat := ecs.Get[components.Creature](w, me).ThreatOf(self)
	otherThreat := ecs.Get[components.Creature](w, me).ThreatOf(other)
	t.Logf("窗口过期后: self(亲自打我)=%d other(通知)=%d 选中=%d", selfThreat, otherThreat, got)
	if got != self {
		t.Fatalf("受击窗口过期后仍应锁定亲自打我的 %d（靠 DirectThreat），实际 %d", self, got)
	}
}
