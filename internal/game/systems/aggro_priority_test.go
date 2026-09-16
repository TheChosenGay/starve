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

// 规则 ②：同时收到**两处**通知时，按【我】到目标的**距离**选最近的。
//
// 注意构造：传播用的是**受害者自己的 AOI 半径**过滤距离，所以两只受害者
// 都必须离我足够近（<6 格）才会真的把仇恨传过来；而两个**攻击者**一近一远，
// 这样才能验证"按我自己的距离选"而不是"按受害者到攻击者的距离选"。
func TestAggroTwoNotificationsPicksNearest(t *testing.T) {
	w := newPackWorld(t)

	nearPl := w.CreateEntity()
	ecs.Add(w, nearPl, components.Player{})
	ecs.Add(w, nearPl, components.Position{X: 13, Y: 11})
	ecs.Add(w, nearPl, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, nearPl, components.Attackable{})

	farPl := w.CreateEntity()
	ecs.Add(w, farPl, components.Player{})
	ecs.Add(w, farPl, components.Position{X: 25, Y: 25})
	ecs.Add(w, farPl, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, farPl, components.Attackable{})

	// 我站在中间；两只"挨打的同伴"都在我身边（否则传播不到我这里）
	me := addWolf(w, 12, 12, 6, nil)
	hurtA := addWolf(w, 11, 12, 6, nil)
	hurtB := addWolf(w, 13, 12, 6, nil)

	// 两只同伴都看得见我和各自的攻击者
	ecs.Get[components.AOI](w, hurtA).Visible = []ecs.Entity{nearPl, me}
	ecs.Get[components.AOI](w, hurtB).Visible = []ecs.Entity{farPl, me}
	// 关键：me **看不见**两个玩家，否则"看见即直接仇恨"会短路掉规则 ②
	ecs.Get[components.AOI](w, me).Visible = nil

	components.Attackable{}.ApplyDamage(w, hurtA, nearPl, 8)
	components.Attackable{}.ApplyDamage(w, hurtB, farPl, 8)

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)

	c := ecs.Get[components.Creature](w, me)
	got := ecs.Get[components.AI](w, me).Target
	nearDist, okNear := c.Indirect[nearPl]
	farDist, okFar := c.Indirect[farPl]
	t.Logf("间接仇恨: nearPl=%d(有=%v) farPl=%d(有=%v) → 选中 %d",
		nearDist, okNear, farDist, okFar, got)
	if !okNear || !okFar {
		t.Fatalf("两处通知都应到达 me：near=%v far=%v", okNear, okFar)
	}
	if nearDist >= farDist {
		t.Fatalf("本测试要求近的攻击者更近：near=%d far=%d", nearDist, farDist)
	}
	if got != nearPl {
		t.Fatalf("应按【我】到目标的距离选最近的(%d)，实际 %d", nearPl, got)
	}
}

// 对抗性验证：让"间接仇恨"的来源**极多且极近**，确认仍然压不过直接仇恨。
//
// 新模型下这不是数值比较，而是语义分级：直接仇恨 > 间接仇恨。
// 但正因为旧实现是比数值（实测 1 vs 125 会选错），这里要钉死"不可能被淹没"。
func TestAggroDirectThreatNeverOvertakenByIndirect(t *testing.T) {
	w := newPackWorld(t)

	self := w.CreateEntity() // 正在打我
	ecs.Add(w, self, components.Player{})
	ecs.Add(w, self, components.Position{X: 12, Y: 10})
	ecs.Add(w, self, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, self, components.Attackable{})

	other := w.CreateEntity() // 只是被别的狼"看到/通知"
	ecs.Add(w, other, components.Player{})
	ecs.Add(w, other, components.Position{X: 10, Y: 10})
	ecs.Add(w, other, components.Health{Max: 100, Cur: 100})
	ecs.Add(w, other, components.Attackable{})

	me := addWolf(w, 12, 11, 6, nil)
	// 大量同伴挨打，全部把 other 传播给我（间接仇恨来源极多）
	for i := 0; i < 5; i++ {
		b := addWolf(w, 11, 10+i%2, 6, nil)
		ecs.Get[components.AOI](w, b).Visible = []ecs.Entity{other, me}
		components.Attackable{}.ApplyDamage(w, b, other, 100)
	}
	ecs.Get[components.AOI](w, me).Visible = []ecs.Entity{self, other}
	// self 亲自打我一下
	components.Attackable{}.ApplyDamage(w, me, self, 1)

	c := ecs.Get[components.Creature](w, me)
	t.Logf("直接仇恨=%d 间接仇恨条数=%d", c.DirectTarget(), len(c.Indirect))
	if !c.IsDirectThreat(self) {
		t.Fatal("前置条件：self 应是直接仇恨")
	}
	if len(c.Indirect) == 0 {
		t.Fatal("前置条件：应存在间接仇恨来源")
	}

	ai := &AISystem{}
	ai.Update(w, 50*time.Millisecond)
	got := ecs.Get[components.AI](w, me).Target
	if got != self {
		t.Fatalf("直接仇恨 %d 不可被任何数量的间接仇恨淹没，实际选中 %d", self, got)
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
