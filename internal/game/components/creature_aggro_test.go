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

	if got := ecs.Get[Creature](w, victim).ThreatOf(player); got != 8 {
		t.Fatalf("受害者本人应获得完整仇恨 8，实际 %d", got)
	}
	if got := ecs.Get[Creature](w, allyWolf).ThreatOf(player); got <= 0 {
		t.Fatalf("同类邻居应获得仇恨，实际 %d", got)
	}
	if got := ecs.Get[Creature](w, allyBoar).ThreatOf(player); got != 0 {
		t.Fatalf("异类不应获得仇恨，实际 %d", got)
	}
}

// 距离越近，分摊到的仇恨越多。
func TestThreatShareDecaysWithDistance(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := addCreature(w, CreatureWolf, 10, 10, 10, nil)
	near := addCreature(w, CreatureWolf, 11, 10, 10, nil) // 距离 1
	far := addCreature(w, CreatureWolf, 18, 10, 10, nil)  // 距离 8
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{near, far}

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 10)

	nearThreat := ecs.Get[Creature](w, near).ThreatOf(player)
	farThreat := ecs.Get[Creature](w, far).ThreatOf(player)
	if nearThreat <= farThreat {
		t.Fatalf("近处同伴应获得更多仇恨：near=%d far=%d", nearThreat, farThreat)
	}
	// 最远处也至少 1（"看见同伴被打"一定要有反应）
	if farThreat < 1 {
		t.Fatalf("范围内的同伴至少应获得 1 点仇恨，实际 %d", farThreat)
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

	if got := ecs.Get[Creature](w, ally).ThreatOf(player); got <= 0 {
		t.Fatalf("直接同伴应获得仇恨，实际 %d", got)
	}
	if got := ecs.Get[Creature](w, farWolf).ThreatOf(player); got != 0 {
		t.Fatalf("同伴的邻居不应被二次传播（会连锁引爆全图），实际 %d", got)
	}
}

// 传播必须**累加**，不能覆盖同伴已有的更高仇恨。
func TestThreatSpreadAccumulates(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := addCreature(w, CreatureWolf, 10, 10, 6, nil)
	ally := addCreature(w, CreatureWolf, 11, 10, 6, nil)
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{ally}

	// 同伴本来就对玩家有很高的仇恨（比如它自己也被打过）
	ecs.Get[Creature](w, ally).Threats[player] = 100
	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 10)

	got := ecs.Get[Creature](w, ally).ThreatOf(player)
	if got <= 100 {
		t.Fatalf("应累加到已有仇恨之上（>100），实际 %d", got)
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

	ecs.Get[Creature](w, victim).AddThreat(w, victim, player, 10)

	if got := ecs.Get[Creature](w, dead).ThreatOf(player); got != 0 {
		t.Fatalf("死亡同类不应获得仇恨，实际 %d", got)
	}
	if got := ecs.Get[Creature](w, offline).ThreatOf(player); got != 0 {
		t.Fatalf("离线同类不应获得仇恨，实际 %d", got)
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
	if got := ecs.Get[Creature](w, victim).ThreatOf(player); got != 10 {
		t.Fatalf("无 AOI 时本人仍应记仇，实际 %d", got)
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

	victimThreat := ecs.Get[Creature](w, victim).ThreatOf(attacker)
	if victimThreat != 10 {
		t.Fatalf("受害者对攻击者的仇恨应恰好为 10（不因自我传播翻倍），实际 %d", victimThreat)
	}
	if got := ecs.Get[Creature](w, attacker).ThreatOf(attacker); got != 0 {
		t.Fatalf("攻击者不应对自己产生仇恨，实际 %d", got)
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
