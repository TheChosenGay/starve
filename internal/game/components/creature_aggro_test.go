package components

import (
	"testing"

	"starve/internal/ecs"
)

// 群体仇恨（Group Aggro）契约测试。
//
// 规则：被攻击的生物把仇恨传播给**感知范围内的同类**。
//   - 只传同类（狼不会因野猪被打而愤怒）
//   - 只传 AOI 范围内的
//   - 越近分摊越多（按切比雪夫距离线性衰减），但至少 1
//   - 累加而非覆盖
//   - **只传播一轮**：被通知的同伴不会二次传播（防连锁引爆全图）

// newAggroWorld 建一个带 codec 的世界。
func newAggroWorld() *ecs.World {
	w := ecs.NewWorld()
	RegisterCodecs(w, false)
	return w
}

// addCreature 建一个生物实体：Creature(kind) + Position + AOI(visible)。
func addCreature(w *ecs.World, kind CreatureKind, x, y, radius int, visible []ecs.Entity) ecs.Entity {
	e := w.CreateEntity()
	ecs.Add(w, e, Creature{Kind: kind, Threats: map[ecs.Entity]int32{}})
	ecs.Add(w, e, Position{X: x, Y: y})
	ecs.Add(w, e, AOI{Radius: radius, Visible: visible})
	return e
}

// 同类邻居应获得仇恨；异类不应获得。
func TestThreatSpreadsToSameKindOnly(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 5, Y: 5})

	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)
	allyWolf := addCreature(w, CreatureWolf, 12, 10, 6, nil)
	allyBoar := addCreature(w, CreatureBoar, 12, 12, 6, nil)
	// 受害者看得见同伴与野猪，以及攻击者位置
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{player, allyWolf, allyBoar}

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 8)

	if got := ecs.Get[Creature](w, victim).DirectTarget(); got != player {
		t.Fatalf("受害者本人应把玩家设为**直接仇恨**，实际 %d", got)
	}
	// 同伴收到的是**间接仇恨**（不是直接仇恨）
	if _, ok := ecs.Get[Creature](w, allyWolf).Indirect[player]; !ok {
		t.Fatal("同类邻居应获得间接仇恨")
	}
	if ecs.Get[Creature](w, allyWolf).IsDirectThreat(player) {
		t.Fatal("同伴只是被通知，不应变成直接仇恨")
	}
	if len(ecs.Get[Creature](w, allyBoar).Indirect) != 0 {
		t.Fatalf("异类不应获得仇恨，实际 %v", ecs.Get[Creature](w, allyBoar).Indirect)
	}
}

// 规则 ②：间接仇恨记录的距离，用于"多来源时选最近的"。
//
// 注意：传播的**门槛**仍按距离（AllyThreatShare，越远分摊越少，
// 超出半径完全不传）；而记录下来的距离是"同伴到目标的距离"，
// 由 AISystem 每 tick 重算（这里只验证传播门槛）。
func TestThreatSpreadRespectsDistance(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := addCreature(w, CreatureWolf, 10, 10, 10, nil)
	near := addCreature(w, CreatureWolf, 11, 10, 10, nil) // 距受害者 1
	edge := addCreature(w, CreatureWolf, 19, 10, 10, nil) // 距受害者 9（半径内）
	out := addCreature(w, CreatureWolf, 25, 10, 10, nil)  // 距受害者 15（半径外）
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{near, edge, out}

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 10)

	if _, ok := ecs.Get[Creature](w, near).Indirect[player]; !ok {
		t.Fatal("半径内的近处同伴应收到间接仇恨")
	}
	if _, ok := ecs.Get[Creature](w, edge).Indirect[player]; !ok {
		t.Fatal("半径边缘（9 < 10）的同伴也应收到间接仇恨")
	}
	if len(ecs.Get[Creature](w, out).Indirect) != 0 {
		t.Fatalf("半径外（15 > 10）的同伴不应收到仇恨，实际 %v",
			ecs.Get[Creature](w, out).Indirect)
	}
	// AllyThreatShare 本身必须随距离单调不增（这是"距离反向"的分摊口径）
	prev := int32(1 << 30)
	for d := 0; d <= 10; d++ {
		cur := AllyThreatShare(10, d, 0, 10)
		if cur > prev {
			t.Fatalf("距离 %d 的分摊 %d 不应大于更近处的 %d", d, cur, prev)
		}
		prev = cur
	}
}

// 超出 AOI 范围的同伴（不在 Visible 里）不应获得仇恨。
func TestThreatDoesNotSpreadBeyondPerception(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)
	outside := addCreature(w, CreatureWolf, 40, 40, 6, nil)
	// 故意不把 outside 放进 Visible：就算坐标很近也不该传播
	near := addCreature(w, CreatureWolf, 11, 10, 6, nil)
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{near} // outside 不在视野内

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 10)

	if got := ecs.Get[Creature](w, outside).ThreatOf(player); got != 0 {
		t.Fatalf("视野外的同类不应获得仇恨，实际 %d", got)
	}
}

// 核心契约：**只传播一轮**。同伴被传播后不得再传给它的邻居。
//
// 防的是"打一只狼 → 全图狼群都冲过来"的连锁爆炸。
//
// 注意构造：第三只狼必须**在同伴的分摊范围内**（距离 < 半径），
// 否则二次传播会因分摊为 0 而无法与"不传播"区分开——
// 那样测试就是假绿的（曾踩过：把 farWolf 放到 (30,30)，距离 20 > 半径 6，
// 于是递归变异体也能通过）。
func TestThreatDoesNotChainPropagate(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)
	ally := addCreature(w, CreatureWolf, 12, 10, 6, nil)
	// 第三只狼紧挨着 ally（距离 1 < 半径 6）：若发生二次传播，它必然获得仇恨
	farWolf := addCreature(w, CreatureWolf, 13, 10, 6, nil)
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{ally}  // 受害者只看得见 ally
	ecs.Get[AOI](w, ally).Visible = []ecs.Entity{farWolf} // farWolf 只在 ally 视野里

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 10)

	if _, ok := ecs.Get[Creature](w, ally).Indirect[player]; !ok {
		t.Fatal("直接同伴应获得间接仇恨")
	}
	if len(ecs.Get[Creature](w, farWolf).Indirect) != 0 {
		t.Fatalf("同伴的邻居不应被二次传播（会连锁引爆全图），实际 %v",
			ecs.Get[Creature](w, farWolf).Indirect)
	}
}

// 规则 ①：同伴若**自己**也被打，会从"间接仇恨"**升级为直接仇恨**
// （间接记录同目标作废），而不是两个数值相加。
func TestDirectThreatSupersedesIndirect(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)
	ally := addCreature(w, CreatureWolf, 11, 10, 6, nil)
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{ally}

	// 先通过传播拿到间接仇恨
	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 10)
	if _, ok := ecs.Get[Creature](w, ally).Indirect[player]; !ok {
		t.Fatal("前置条件：同伴应已有间接仇恨")
	}

	// 同伴自己也被打了 → 升级为直接仇恨
	ecs.Get[Creature](w, ally).AddThreat(w, ally, player, 10)
	c := ecs.Get[Creature](w, ally)
	if c.DirectTarget() != player {
		t.Fatal("同伴自己被打后应升级为直接仇恨")
	}
	if _, ok := c.Indirect[player]; ok {
		t.Fatal("已升级为直接仇恨后，同目标的间接记录应作废（规则 ②的'覆盖'）")
	}
}

// 死亡/离线的同类不应被传播（避免给尸体记仇、或唤醒离线实体）。
func TestThreatSkipsDeadAndOfflineAllies(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)
	dead := addCreature(w, CreatureWolf, 11, 10, 6, nil)
	offline := addCreature(w, CreatureWolf, 11, 11, 6, nil)
	ecs.Add(w, dead, Dead{})
	ecs.Add(w, offline, Offline{})
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{dead, offline}

	// 用真实伤害路径触发（AddThreat 也可，但 ApplyDamage 更贴近实际）
	victimE := victim
	ecs.Add(w, victimE, Health{Max: 50, Cur: 50})
	ecs.Add(w, victimE, Attackable{})
	Attackable{}.ApplyDamage(w, victimE, player, 10)

	// 断言必须看 Indirect（真正的仇恨表），不能看 ThreatOf：
	// ThreatOf 是数值投影，无论是否"被跳过"都可能是 0，用它断言测不出回归
	// （变异测试证实过：去掉死亡/离线护栏后该断言仍然通过）。
	if n := len(ecs.Get[Creature](w, dead).Indirect); n != 0 {
		t.Fatalf("死亡同类不应获得仇恨，实际 Indirect=%v", ecs.Get[Creature](w, dead).Indirect)
	}
	if n := len(ecs.Get[Creature](w, offline).Indirect); n != 0 {
		t.Fatalf("离线同类不应获得仇恨，实际 Indirect=%v", ecs.Get[Creature](w, offline).Indirect)
	}
}

// 受害者没有 AOI（无感知）时不应 panic，也不传播。
func TestThreatSpreadWithoutAOIIsSafe(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := w.CreateEntity()
	ecs.Add(w, victim, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})
	ecs.Add(w, victim, Position{X: 10, Y: 10})
	// 无 AOI

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 10)
	if got := ecs.Get[Creature](w, victim).DirectTarget(); got != player {
		t.Fatalf("无 AOI 时本人仍应记仇（直接仇恨），实际 %d", got)
	}
}

// 攻击者本身若恰好在 Visible 里且是同类，不应给自己传播（自我传播无意义且会翻倍）。
func TestThreatDoesNotSpreadToAttackerOrSelf(t *testing.T) {
	w := newAggroWorld()
	// 用同类生物当攻击者（狼打狼），覆盖 ally == attacker 的分支
	attacker := addCreature(w, CreatureWolf, 11, 10, 6, nil)
	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{attacker, victim}

	ecs.Get[Creature](w, victim).AddThreat(w, victim, attacker, 10)

	if got := ecs.Get[Creature](w, victim).DirectTarget(); got != attacker {
		t.Fatalf("受害者应把攻击者设为直接仇恨，实际 %d", got)
	}
	if got := ecs.Get[Creature](w, attacker).DirectTarget(); got != 0 {
		t.Fatalf("攻击者不应对自己产生仇恨，实际 %d", got)
	}
	if len(ecs.Get[Creature](w, attacker).Indirect) != 0 {
		t.Fatalf("攻击者不应对自己产生间接仇恨，实际 %v", ecs.Get[Creature](w, attacker).Indirect)
	}
}

// AllyThreatShare 的边界：范围外 = 0，范围内至少 1，且不超过伤害值。
func TestAllyThreatShareBounds(t *testing.T) {
	if got := AllyThreatShare(10, 7, 0, 6); got != 0 {
		t.Fatalf("范围外应为 0，实际 %d", got)
	}
	if got := AllyThreatShare(1, 6, 0, 6); got < 1 {
		t.Fatalf("范围边界上至少应为 1，实际 %d", got)
	}
	if got := AllyThreatShare(10, 0, 0, 6); got != 10 {
		t.Fatalf("同格应为全额 10，实际 %d", got)
	}
	// 切比雪夫距离：对角 (4,4) 的距离是 4，与 (4,0) 相同
	if a, b := AllyThreatShare(10, 4, 4, 10), AllyThreatShare(10, 4, 0, 10); a != b {
		t.Fatalf("切比雪夫距离应对角与正交一致：%d vs %d", a, b)
	}
	if got := AllyThreatShare(0, 0, 0, 6); got != 0 {
		t.Fatalf("0 伤害不应传播，实际 %d", got)
	}
}

// DirectThreat 契约：**亲自打我**的才进这张表，群体仇恨通知不进去。
//
// 这是"正在打我的人优先于通知来的人"的基础：两者必须可区分，
// 否则选目标时只能比数值大小，会被通知的叠加值淹没。
func TestDirectThreatOnlyForSelfAttacks(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)
	ally := addCreature(w, CreatureWolf, 12, 10, 6, nil)
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{ally}

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 8)

	if !ecs.Get[Creature](w, victim).IsDirectThreat(player) {
		t.Fatal("受害者本人应把攻击者记为 DirectThreat")
	}
	if ecs.Get[Creature](w, ally).IsDirectThreat(player) {
		t.Fatal("被通知的同伴**不应**把该攻击者记为 DirectThreat（那只是通知）")
	}
	if got := ecs.Get[Creature](w, ally).ThreatOf(player); got <= 0 {
		t.Fatalf("同伴仍应获得仇恨值，实际 %d", got)
	}
}

// DirectThreat 必须持久化：否则读档后"正在打我的人"优先级丢失，
// 表现为读档瞬间目标从"打我的人"跳到"通知来的人"。
func TestDirectThreatSurvivesCodecRoundTrip(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})
	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 8)

	var codec creatureCodec
	raw, err := codec.Encode(*ecs.Get[Creature](w, victim))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := codec.Decode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !back.IsDirectThreat(player) {
		t.Fatal("DirectThreat 应能经过 codec 往返保留")
	}
	if back.DirectTarget() != player {
		t.Fatalf("直接仇恨对象应经 codec 往返保留，实际 %d", back.DirectTarget())
	}
}

// ── 组件契约（见 docs/仇恨传播-组件契约.md）────────────────────
//
// 这组测试把"参与群体仇恨需要哪些组件"钉成可执行契约。
//
// 为什么值得单独测：漏挂组件的表现是**静默失效**——不报错、不崩溃，
// 只是"打了没反应"。加新生物/新技能时最容易踩，且极难排查。

// 最小发送方组件集：Position + Health + Attackable + Creature + AOI。
func TestMinimalComponentSetCanPropagate(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	mk := func(x, y int) ecs.Entity {
		e := w.CreateEntity()
		ecs.Add(w, e, Position{X: x, Y: y})
		ecs.Add(w, e, Health{Max: 50, Cur: 50})
		ecs.Add(w, e, Attackable{})
		ecs.Add(w, e, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})
		ecs.Add(w, e, AOI{Radius: 8, Perception: 6, Threat: 8})
		return e
	}
	victim, ally, far := mk(10, 10), mk(12, 10), mk(14, 10)

	// AOI.Visible 平时由 AOISystem 填；这里手工设成"受害者看得见 ally"
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{player, ally}
	ecs.Get[AOI](w, ally).Visible = []ecs.Entity{player, far}

	Attackable{}.ApplyDamage(w, victim, player, 10)

	if got := ecs.Get[Creature](w, victim).DirectTarget(); got != player {
		t.Fatal("发送方应获得直接仇恨")
	}
	if _, ok := ecs.Get[Creature](w, ally).Indirect[player]; !ok {
		t.Fatal("同伴应获得间接仇恨（最小组件集即可）")
	}
	if len(ecs.Get[Creature](w, far).Indirect) != 0 {
		t.Fatal("不应二次传播（只传一轮）")
	}
}

// 缺任一关键组件都**不会**传播——逐项锁死，防止将来有人把某个前置检查
// 挪走或放宽，让"不该传播的东西"开始传播。
func TestMissingComponentBlocksPropagation(t *testing.T) {
	cases := []struct {
		name          string
		creature, aoi bool
	}{
		{"缺 Creature", false, true},
		{"缺 AOI", true, false},
		{"两者都缺", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newAggroWorld()
			player := w.CreateEntity()
			ecs.Add(w, player, Position{X: 0, Y: 0})

			victim := w.CreateEntity()
			ecs.Add(w, victim, Position{X: 10, Y: 10})
			ecs.Add(w, victim, Health{Max: 100, Cur: 100})
			ecs.Add(w, victim, Attackable{})
			if c.creature {
				ecs.Add(w, victim, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})
			}
			if c.aoi {
				ecs.Add(w, victim, AOI{Radius: 6, Perception: 6, Threat: 6})
			}
			ally := addCreature(w, CreatureWolf, 11, 10, 6, nil)
			if c.aoi {
				ecs.Get[AOI](w, victim).Visible = []ecs.Entity{player, ally}
			}

			Attackable{}.ApplyDamage(w, victim, player, 10)
			if n := len(ecs.Get[Creature](w, ally).Indirect); n != 0 {
				t.Fatalf("缺组件时不应传播，实际 %d 条间接仇恨", n)
			}
		})
	}
}

// 接收方不需要 Attackable / AOI：和平生物一样会被通知。
//
// 这是**有意**的语义（"记恨上打我同伴的人"不需要我自己能打架），
// 但值得钉住——若将来改成"只有掠食者才参与群体仇恨"，
// 这条测试会失败，提醒改动者确认这是想要的行为。
func TestReceiverNeedsNoAttackableOrAOI(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	// 受害者：完整组件集（能被伤害，才能触发传播）
	victim := w.CreateEntity()
	ecs.Add(w, victim, Position{X: 10, Y: 10})
	ecs.Add(w, victim, Health{Max: 50, Cur: 50})
	ecs.Add(w, victim, Attackable{})
	ecs.Add(w, victim, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})
	ecs.Add(w, victim, AOI{Radius: 8, Perception: 6, Threat: 8})

	// 同伴：只有 Position + Health + Creature（**没有** Attackable / AOI）
	passive := w.CreateEntity()
	ecs.Add(w, passive, Position{X: 12, Y: 10})
	ecs.Add(w, passive, Health{Max: 20, Cur: 20})
	ecs.Add(w, passive, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})

	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{player, passive}
	Attackable{}.ApplyDamage(w, victim, player, 10)

	if _, ok := ecs.Get[Creature](w, passive).Indirect[player]; !ok {
		t.Fatal("没有 Attackable/AOI 的同类也应被通知（接收方只需 Position+Health+Creature）")
	}
}

// 传播半径显式取 Threat，不回退到 Radius 时"碰巧"生效。
//
// 构造 Threat < Radius：只有 Threat 半径内的同类才该被通知。
// 若实现误用 Radius，远处的同类也会收到 → 测试失败。
func TestPropagationUsesThreatRadiusNotCoverageRadius(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := w.CreateEntity()
	ecs.Add(w, victim, Position{X: 10, Y: 10})
	ecs.Add(w, victim, Health{Max: 50, Cur: 50})
	ecs.Add(w, victim, Attackable{})
	ecs.Add(w, victim, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})
	// Radius 很大（20），但真正的传播半径只有 3
	ecs.Add(w, victim, AOI{Radius: 20, Perception: 6, Threat: 3})

	near := addCreature(w, CreatureWolf, 12, 10, 20, nil)    // 距离 2（<=3，应通知）
	farAway := addCreature(w, CreatureWolf, 18, 10, 20, nil) // 距离 8（>3，不该通知）

	// 手工把两者都放进 Visible（模拟 Radius=20 的大范围覆盖）
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{player, near, farAway}

	Attackable{}.ApplyDamage(w, victim, player, 10)

	if _, ok := ecs.Get[Creature](w, near).Indirect[player]; !ok {
		t.Fatal("Threat 半径内的同类应被通知")
	}
	if n := len(ecs.Get[Creature](w, farAway).Indirect); n != 0 {
		t.Fatal("超出 Threat 半径的同类的**不该**被通知（说明误用了 Radius）")
	}
}

// 邻居**缺 Creature** 时必须安全跳过，不能崩。
//
// 这一条防的是 panic：`ecs.Get[Creature]` 在组件缺失时**会 panic**
// （不是返回零值，见 sparse_set.go）。所以 `SpreadThreatToAllies` 里那句
// `!ecs.Has[Creature](w, ally)` 是**必需的护栏**，不是冗余检查。
//
// 现实场景：玩家/掉落物/建筑都会出现在 Visible 里，它们没有 Creature。
// 少了这个护栏，一次普通攻击就能让服务器 panic。
func TestAllyWithoutCreatureIsSkippedSafely(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := w.CreateEntity()
	ecs.Add(w, victim, Position{X: 10, Y: 10})
	ecs.Add(w, victim, Health{Max: 50, Cur: 50})
	ecs.Add(w, victim, Attackable{})
	ecs.Add(w, victim, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})
	ecs.Add(w, victim, AOI{Radius: 8, Perception: 6, Threat: 8})

	// 各种"没有 Creature"的邻居：只有 Position（有的连 Health 都没有）
	bare := w.CreateEntity()
	ecs.Add(w, bare, Position{X: 11, Y: 10})

	withHP := w.CreateEntity()
	ecs.Add(w, withHP, Position{X: 12, Y: 10})
	ecs.Add(w, withHP, Health{Max: 10, Cur: 10})

	// 一只正常的同类邻居作为对照（确认传播本身没被影响）
	ally := w.CreateEntity()
	ecs.Add(w, ally, Position{X: 13, Y: 10})
	ecs.Add(w, ally, Health{Max: 30, Cur: 30})
	ecs.Add(w, ally, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})

	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{player, bare, withHP, ally}

	// 不能 panic
	Attackable{}.ApplyDamage(w, victim, player, 10)

	if _, ok := ecs.Get[Creature](w, ally).Indirect[player]; !ok {
		t.Fatal("正常同类邻居仍应收到仇恨（护栏不能误伤有效目标）")
	}
}
