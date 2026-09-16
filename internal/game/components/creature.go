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

// Creature 生物身份与长期状态（类型 + 仇恨表 + 出生点/游荡）。
// Drops 仅保留用于旧存档迁移；新实体通过 DropSource 按配置表解析掉落。
type Creature struct {
	Kind         CreatureKind
	Threats      map[ecs.Entity]int32 // 仇恨表（实体 → 威胁值）
	HomeX, HomeY int                  // 出生点（游荡锚点）
	RoamRadius   int
	Drops        []ItemStack // 旧存档中的固定死亡掉落
}

// ThreatOf 返回对某实体的仇恨值。
func (c *Creature) ThreatOf(e ecs.Entity) int32 { return c.Threats[e] }

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

// AddThreat 增加对攻击者的仇恨（按实际造成伤害）。由 Attackable.ApplyDamage 调用。
//
// 除了自己记仇，还会把仇恨**传播给感知范围内的同类**（群体仇恨，见 SpreadThreatToAllies）。
func (c *Creature) AddThreat(w *ecs.World, e ecs.Entity, attacker ecs.Entity, amount int32) {
	if c.Threats == nil {
		c.Threats = map[ecs.Entity]int32{}
	}
	c.Threats[attacker] += amount
	ecs.MarkDirty[Creature](w, e)
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
		radius := 0
		if ecs.Has[AOI](w, ally) {
			radius = ecs.Get[AOI](w, ally).Radius
		} else if ecs.Has[AOI](w, victim) {
			radius = ecs.Get[AOI](w, victim).Radius
		}
		share := AllyThreatShare(amount, ap.X-vPos.X, ap.Y-vPos.Y, radius)
		if share <= 0 {
			continue
		}
		// 累加而非覆盖：不抹掉同伴自己对同一目标已有的更高仇恨
		if a.Threats == nil {
			a.Threats = map[ecs.Entity]int32{}
		}
		a.Threats[attacker] += share
		ecs.MarkDirty[Creature](w, ally)
	}
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
		HomeX:      int(m.HomeX),
		HomeY:      int(m.HomeY),
		RoamRadius: int(m.RoamRadius),
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
