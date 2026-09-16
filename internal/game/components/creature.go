package components

import (
	"sort"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// CreatureKind 生物类型（单一事实来源 = proto 枚举；配置用名字，内部用枚举）。
type CreatureKind = game.CreatureKind

// 常用生物类型常量。
const (
	CreatureRabbit = game.CreatureKind_CREATURE_KIND_RABBIT
	CreatureWolf   = game.CreatureKind_CREATURE_KIND_WOLF
	CreatureBoar   = game.CreatureKind_CREATURE_KIND_BOAR
	CreatureDeer   = game.CreatureKind_CREATURE_KIND_DEER
	CreatureSpider = game.CreatureKind_CREATURE_KIND_SPIDER
)

// CreatureKindByName 配置字符串 → 生物类型（新生物 = 枚举值 + 这里加一行 + creatures.json）。
var CreatureKindByName = map[string]CreatureKind{
	"rabbit": CreatureRabbit,
	"wolf":   CreatureWolf,
	"boar":   CreatureBoar,
	"deer":   CreatureDeer,
	"spider": CreatureSpider,
}

// Creature 生物身份与长期状态（类型 + 仇恨 + 出生点/游荡）。
// Drops 仅保留用于旧存档迁移；新实体通过 DropSource 按配置表解析掉落。
//
// # 仇恨模型（两类，语义完全不同）
//
//	① **直接仇恨** Direct —— 谁**亲自**攻击过我。它是一个**结构体**（攻击者是谁），
//	   不是一个可以互相比较大小的数值。语义上"最大"，**不可被任何传播来的仇恨替换**；
//	   只有"另一个对象亲自攻击了我"才会更新它（换成那个新的攻击者）。
//
//	② **间接仇恨** Indirect —— 同伴被打后**传播**过来的。它不是固定数值，
//	   而是**随距离反向、随时会变**的量（越近越大）；每当被攻击者位置变化、
//	   或攻击者位置变化，就需要重算。多个来源时按距离远近选择；
//	   若此时我被亲自攻击（升级为直接仇恨），或来了**更近**的传播源 → 覆盖当前。
//
//	③ 感知范围内既无敌对目标、也无间接仇恨对象 → **清空仇恨**
//	   （敌人跑出范围或死亡即遗忘）。
//
// 为什么直接仇恨不参与数值比较：群体仇恨按伤害分摊，多个同伴被同一人打时
// 叠加值可以轻易超过"正在打我的人"（实测 1 vs 125）。纯比数值会让生物
// **抛下正在揍它的敌人**去打远处的——与直觉和玩法都相反。
type Creature struct {
	Kind         CreatureKind
	HomeX, HomeY int // 出生点（游荡锚点）
	RoamRadius   int
	Drops        []ItemStack // 旧存档中的固定死亡掉落

	// Direct 是**直接仇恨**：谁亲自攻击过我。见类型注释的 ①。
	// 仅由受击路径（AddThreat）写入，**不参与传播**，也不随距离变化。
	Direct ecs.Entity

	// Indirect 是**间接仇恨**：同伴被打后传播来的目标 → 该目标在我感知里时的距离。
	// 见类型注释的 ②。距离每次重算，值越小（越近）优先级越高。
	// 空 map = 没有间接仇恨。
	Indirect map[ecs.Entity]int

	// Threats 是**给客户端/存档的仇恨值投影**，由上面两者派生（见 SyncThreats）。
	//
	// 为什么保留它：快照协议（game.Creature.threats）是既有契约，客户端
	// 与存档都依赖它；而真正的决策只用 Direct/Indirect。两者在本文件里
	// 同步维护，避免"决策改了但协议没改"的脱节。
	Threats map[ecs.Entity]int32
}

// ThreatOf 返回对某实体的仇恨值（协议投影，不用于决策）。
func (c *Creature) ThreatOf(e ecs.Entity) int32 { return c.Threats[e] }

// DirectTarget 返回当前的直接仇恨对象（0 = 无）。
func (c *Creature) DirectTarget() ecs.Entity { return c.Direct }

// IsDirectThreat 报告 e 是否是当前的**直接仇恨**对象（亲自攻击过我）。
func (c *Creature) IsDirectThreat(e ecs.Entity) bool {
	return e != 0 && c.Direct == e
}

// HasAnyThreat 报告是否存在任何仇恨（直接或间接）。
func (c *Creature) HasAnyThreat() bool { return c.Direct != 0 || len(c.Indirect) > 0 }

// SetDirectThreat 设置**直接仇恨**：e 亲自攻击了我。
//
// 规则 ①：直接仇恨不可被传播覆盖，但**会被新的亲自攻击者更新**——
// 所以这里是无条件赋值（后打的顶掉先打的），这正是"另一个对象攻击了
// 当前生物触发更新"的语义。
func (c *Creature) SetDirectThreat(w *ecs.World, self, e ecs.Entity) {
	if e == 0 {
		return
	}
	c.Direct = e
	// 直接仇恨降临时，间接仇恨里同一个目标已经没有必要保留（直接优先）
	if c.Indirect != nil {
		delete(c.Indirect, e)
	}
	c.SyncThreats(w, self)
}

// NoteIndirect 记录/更新一个**间接仇恨**来源及其距离（规则 ②）。
//
// 覆盖规则（用户明确要求）：来了**更近**的传播源就覆盖当前的。
// 这里按"每个目标各记一条距离"实现，选目标时再取最近的——
// 等价于"更近的覆盖当前的"，但不会因为目标暂时变远就把记录丢掉，
// 目标走远了只是优先级下降，符合"仇恨值随距离变化"的语义。
func (c *Creature) NoteIndirect(w *ecs.World, self, e ecs.Entity) {
	if e == 0 || e == c.Direct {
		return // 已经是直接仇恨，间接记录无意义
	}
	if c.Indirect == nil {
		c.Indirect = map[ecs.Entity]int{}
	}
	if _, ok := c.Indirect[e]; ok {
		return // 已在表里；距离由 AISystem 每 tick 刷新
	}
	// 先占位 0（= 最近，最优先）；AISystem 会在同一 tick 内算出真实距离。
	// 不在这里算距离：本函数拿不到同伴与目标的相对位置语义（见调用处注释）。
	c.Indirect[e] = 0
	c.SyncThreats(w, self)
}

// ClearThreats 清空全部仇恨（规则 ③：没敌对了就遗忘）。
func (c *Creature) ClearThreats(w *ecs.World, self ecs.Entity) bool {
	if !c.HasAnyThreat() {
		return false
	}
	c.Direct = 0
	if c.Indirect != nil {
		clear(c.Indirect)
	}
	c.SyncThreats(w, self)
	return true
}

// DropThreat 移除某个目标的仇恨（死亡/跑出范围/超出拴绳）。
func (c *Creature) DropThreat(w *ecs.World, self, e ecs.Entity) bool {
	changed := false
	if c.Direct == e {
		c.Direct = 0
		changed = true
	}
	if c.Indirect != nil {
		if _, ok := c.Indirect[e]; ok {
			delete(c.Indirect, e)
			changed = true
		}
	}
	if changed {
		c.SyncThreats(w, self)
	}
	return changed
}

// SyncThreats 把 Direct/Indirect 投影成协议用的 Threats 数值表并标脏。
//
// 数值只是**表现/存档用**，不参与决策：
//   - 直接仇恨给一个明显高的基数（让 UI/存档能看出"这是直接仇恨"）；
//   - 间接仇恨按距离反向给分（越近越高），与"仇恨随距离变化"的语义一致。
func (c *Creature) SyncThreats(w *ecs.World, self ecs.Entity) {
	if c.Threats == nil {
		c.Threats = map[ecs.Entity]int32{}
	}
	clear(c.Threats)
	if c.Direct != 0 {
		c.Threats[c.Direct] = DirectThreatScore
	}
	for e, d := range c.Indirect {
		if e == c.Direct {
			continue
		}
		if s := IndirectThreatScore(d); s > 0 {
			c.Threats[e] = s
		}
	}
	ecs.MarkDirty[Creature](w, self)
}

// DirectThreatScore 是直接仇恨在协议投影里的固定分值（仅供 UI/存档区分，
// 不参与决策——决策永远优先 Direct）。
const DirectThreatScore = 1000

// IndirectThreatScore 把"到间接仇恨源的距离"折算成协议投影分值。
//
// 距离越近分越高（用户要求的"反向关系"）。仅用于投影，
// 真实决策按距离排序，不依赖这个映射。
func IndirectThreatScore(dist int) int32 {
	const base = MaxIndirectDistance
	if dist < 0 {
		dist = 0
	}
	if dist > base {
		return 0
	}
	return int32(base - dist)
}

// MaxIndirectDistance 是间接仇恨的有效距离上限（格）。
// 超过它视为"跑出仇恨范围"（规则 ③ 的清空条件之一）。
const MaxIndirectDistance = 32

// AllyThreatShare 是"群体仇恨"分摊到单个同伴的仇恨值。
//
// 设计取舍：
//   - **按距离线性衰减**：越近的同伴反应越强（`1 - dist/radius`），
//     最远处仍至少给 1，保证"看见同伴被打"一定有反应，不会因距离算成 0。
//   - 上限取被击者本次实际伤害：同伴的愤怒不应超过受害者本人。
//
// 用切比雪夫距离（max(|dx|,|dy|)）而非欧氏距离：AOI 感知是**正方形**范围，
// 用欧氏距离会出现"视觉上在范围内、判定却在范围外"的不一致。
func AllyThreatShare(amount int32, dx, dy, radius int) int32 {
	if amount <= 0 || radius <= 0 {
		return 0
	}
	d := dx
	if d < 0 {
		d = -d
	}
	if dy2 := dy; dy2 < 0 {
		if -dy2 > d {
			d = -dy2
		}
	} else if dy2 > d {
		d = dy2
	}
	if d > radius {
		return 0
	}
	// amount × (radius - d) / radius，至少 1
	share := int32(int64(amount) * int64(radius-d) / int64(radius))
	if share < 1 {
		share = 1
	}
	return share
}

// AddThreat 处理"我被 attacker 亲自打了"（规则 ①）。由 Attackable.ApplyDamage 调用。
//
// 语义：把 attacker 设为**直接仇恨**。直接仇恨不可被传播覆盖，
// 但会被**新的亲自攻击者**更新——所以这里是赋值，不是累加数值。
//
// 同时把这次受击**传播给感知范围内的同类**（群体仇恨，见 SpreadThreatToAllies），
// 同伴那边收到的是**间接仇恨**（规则 ②）。
func (c *Creature) AddThreat(w *ecs.World, e ecs.Entity, attacker ecs.Entity, amount int32) {
	c.SetDirectThreat(w, e, attacker)
	SpreadThreatToAllies(w, e, attacker, amount)
}

// SpreadThreatToAllies 把"某同类被 amount 点伤害"传播给感知范围内的同类。
//
// 这是**群体仇恨**的核心：打一只狼，附近的狼一起记仇。
//
// 为什么只传播一轮（不会连锁引爆全图）：
//
//	传播只发生在**真实受击**时（ApplyDamage → AddThreat → 本函数）。
//	同伴收到的只是仇恨表里的一个数值，并不会再次调用本函数——
//	"被通知"与"被打"是两件事。因此不存在 A 传 B、B 传 C 的递归。
//
// 同类 = 相同 Creature.Kind；拦在 AOI.Visible 里做，无需全局扫描。
// 确定性：AOI.Visible 本身已按实体 id 升序，"谁的仇恨先加"不影响最终值（累加）。
func SpreadThreatToAllies(w *ecs.World, victim, attacker ecs.Entity, amount int32) {
	if amount <= 0 || !ecs.Has[AOI](w, victim) {
		return
	}
	vKind := ecs.Get[Creature](w, victim).Kind
	vPos := ecs.Get[Position](w, victim)
	for _, ally := range ecs.Get[AOI](w, victim).Visible {
		if ally == victim || ally == attacker {
			continue
		}
		if !w.IsAlive(ally) || ecs.Has[Dead](w, ally) || ecs.Has[Offline](w, ally) {
			continue
		}
		if !ecs.Has[Creature](w, ally) || !ecs.Has[Position](w, ally) {
			continue
		}
		a := ecs.Get[Creature](w, ally)
		// 只传播给同类：狼不会因为野猪被打而愤怒
		if a.Kind != vKind {
			continue
		}
		ap := ecs.Get[Position](w, ally)
		// 用**仇恨传播半径**（不是感知半径、也不是 Visible 的覆盖半径）。
		//
		// 显式取 Threat 而不是 Radius：Radius 恰好 = max(感知, 威胁)，
		// 用它"碰巧也对"，但语义不清——将来若有人调整这个 max 的算法，
		// 传播范围就会被无声地改掉。
		radius := 0
		if ecs.Has[AOI](w, ally) {
			radius = ecs.Get[AOI](w, ally).ThreatRadius()
		}
		if radius <= 0 && ecs.Has[AOI](w, victim) {
			radius = ecs.Get[AOI](w, victim).ThreatRadius()
		}
		share := AllyThreatShare(amount, ap.X-vPos.X, ap.Y-vPos.Y, radius)
		if share <= 0 {
			continue
		}
		// 同伴收到的是**间接仇恨**（规则 ②）：只记录"攻击者是谁"。
		//
		// 距离**不在这里算**：规则 ② 要求的是"仇恨随【同伴自己】到目标的距离
		// 反向变化"，而这里拿到的是受害者到攻击者的距离。两者是不同的量
		// （实测踩过：存了后者，导致距离永远不变、也无法按远近选目标）。
		// 距离由 AISystem 每 tick 按同伴的当前位置重算。
		//
		// 注意**不是**直接仇恨——它是通知，不是"亲自打我"。
		// 若该同伴后来自己也被打，AddThreat 会把它升级为直接仇恨（规则 ①）。
		a.NoteIndirect(w, ally, attacker)
	}
}

// chebyshev 切比雪夫距离（max(|dx|,|dy|)）：
// 与 AOI 的正方形感知范围口径一致，避免"视觉上在范围内、判定却在范围外"。
func chebyshev(ax, ay, bx, by int) int {
	dx := ax - bx
	if dx < 0 {
		dx = -dx
	}
	dy := ay - by
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		return dy
	}
	return dx
}

type creatureCodec struct{}

func (creatureCodec) Encode(v Creature) ([]byte, error) {
	out := &game.Creature{
		Kind:       v.Kind,
		HomeX:      int32(v.HomeX),
		HomeY:      int32(v.HomeY),
		RoamRadius: int32(v.RoamRadius),
	}
	// 仇恨表按实体 id 排序编码（确定性）
	ids := make([]int, 0, len(v.Threats))
	for e, t := range v.Threats {
		if t > 0 {
			ids = append(ids, int(e))
		}
	}
	sort.Ints(ids)
	for _, id := range ids {
		out.Threats = append(out.Threats, &game.ThreatEntry{EntityId: uint64(id), Threat: v.Threats[ecs.Entity(id)]})
	}
	// 直接仇恨（亲自打我的人）。必须持久化：否则重启后优先级丢失，
	// 表现为读档瞬间目标从"打我的人"跳到"通知来的人"。
	out.DirectThreat = uint64(v.Direct)
	// 间接仇恨按实体 id 升序编码（确定性）
	ind := make([]int, 0, len(v.Indirect))
	for e := range v.Indirect {
		ind = append(ind, int(e))
	}
	sort.Ints(ind)
	for _, id := range ind {
		out.IndirectThreats = append(out.IndirectThreats, &game.IndirectThreat{
			EntityId: uint64(id),
			Distance: int32(v.Indirect[ecs.Entity(id)]),
		})
	}
	out.Drops = slotsToProto(v.Drops)
	return pb.Marshal(out)
}

func (creatureCodec) Decode(b []byte) (Creature, error) {
	var m game.Creature
	if err := pb.Unmarshal(b, &m); err != nil {
		return Creature{}, err
	}
	out := Creature{
		Kind:       m.Kind,
		Threats:    map[ecs.Entity]int32{},
		Indirect:   map[ecs.Entity]int{},
		Direct:     ecs.Entity(m.DirectThreat),
		HomeX:      int(m.HomeX),
		HomeY:      int(m.HomeY),
		RoamRadius: int(m.RoamRadius),
	}
	for _, it := range m.IndirectThreats {
		if it != nil {
			out.Indirect[ecs.Entity(it.EntityId)] = int(it.Distance)
		}
	}
	// 旧存档兼容：只有 Threats 数值表、没有 Direct/Indirect 时，
	// 把数值最高的那个当作直接仇恨（至少不会读档后完全丢失攻击者）。
	if out.Direct == 0 && len(m.IndirectThreats) == 0 {
		best := ecs.Entity(0)
		var bestV int32
		for _, t := range m.Threats {
			if t != nil && t.Threat > bestV {
				bestV, best = t.Threat, ecs.Entity(t.EntityId)
			}
		}
		out.Direct = best
	}
	for _, t := range m.Threats {
		if t != nil && t.Threat > 0 {
			out.Threats[ecs.Entity(t.EntityId)] = t.Threat
		}
	}
	for _, s := range m.Drops {
		if s != nil {
			out.Drops = append(out.Drops, ItemStack{Kind: s.Kind, Count: int(s.Count), MaxStack: int(s.MaxStack), Durability: int(s.Durability)})
		}
	}
	return out, nil
}

func RegisterCreature(w *ecs.World) {
	ecs.RegisterComponent(w, "Creature", creatureCodec{})
}
