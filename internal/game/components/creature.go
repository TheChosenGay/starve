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
)

// CreatureKindByName 配置字符串 → 生物类型（新生物 = 枚举值 + 这里加一行 + creatures.json）。
var CreatureKindByName = map[string]CreatureKind{
	"rabbit": CreatureRabbit,
	"wolf":   CreatureWolf,
}

// Creature 生物身份与长期状态（类型 + 仇恨表 + 出生点/游荡 + 掉落）。
// 行为状态在 AI 组件，攻击能力在 Weapon 组件。
type Creature struct {
	Kind         CreatureKind
	Threats      map[ecs.Entity]int32 // 仇恨表（实体 → 威胁值）
	HomeX, HomeY int                  // 出生点（游荡锚点）
	RoamRadius   int
	Drops        []ItemStack // 死亡掉落
}

// ThreatOf 返回对某实体的仇恨值。
func (c *Creature) ThreatOf(e ecs.Entity) int32 { return c.Threats[e] }

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
