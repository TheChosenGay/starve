package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Health 生命值（纯数据，无锁）。防御百分比由装备/效果叠加，攻击减免在 TakeDamage。
type Health struct {
	Cur            int
	Max            int
	DefensePercent int // 攻击时按此百分比减免（0-100）
}

// TakeDamage 受到 attack 点攻击：按防御百分比减免后扣血，返回实际扣血量。
func (h *Health) TakeDamage(attack int) int {
	dealt := attack * (100 - h.DefensePercent) / 100
	if dealt < 0 {
		dealt = 0
	}
	if dealt > h.Cur {
		dealt = h.Cur
	}
	h.Cur -= dealt
	return dealt
}

// ApplyDamage 世界级攻击结算：减免扣血 + 受击副作用（AI 标记/仇恨/打断）。
// 被攻击后的反馈与 Health 内聚：检查实体是否有 AI/Creature 组件并标记。
// 由交互层（interactive 的 attack pair）调用。
func ApplyDamage(w *ecs.World, target, attacker ecs.Entity, attack int) int {
	if attack <= 0 || !w.IsAlive(target) || ecs.Has[Dead](w, target) || !ecs.Has[Health](w, target) {
		return 0
	}
	hp := ecs.Get[Health](w, target)
	dealt := hp.TakeDamage(attack)
	ecs.MarkDirty[Health](w, target)

	// 受击标记（AI 输入：本 tick 被谁打）
	if ecs.Has[AI](w, target) {
		ai := ecs.Get[AI](w, target)
		ai.LastHitBy = attacker
		if dc, ok := ecs.TryResource[DayCycle](w); ok {
			ai.LastHitAt = dc.Phase
		}
		ecs.MarkDirty[AI](w, target)
	}
	// 仇恨（被打的生物记仇，按实际伤害）
	if ecs.Has[Creature](w, target) {
		c := ecs.Get[Creature](w, target)
		if c.Threats == nil {
			c.Threats = map[ecs.Entity]int32{}
		}
		c.Threats[attacker] += int32(dealt)
		ecs.MarkDirty[Creature](w, target)
	}
	// 受击打断（制作等）
	TryInterrupt(w, target)
	return dealt
}

type healthCodec struct{}

func (healthCodec) Encode(v Health) ([]byte, error) {
	return pb.Marshal(&game.Health{Cur: int32(v.Cur), Max: int32(v.Max), DefensePercent: int32(v.DefensePercent)})
}

func (healthCodec) Decode(b []byte) (Health, error) {
	var h game.Health
	if err := pb.Unmarshal(b, &h); err != nil {
		return Health{}, err
	}
	return Health{Cur: int(h.Cur), Max: int(h.Max), DefensePercent: int(h.DefensePercent)}, nil
}

func RegisterHealth(w *ecs.World) {
	ecs.RegisterComponent(w, "Health", healthCodec{})
}
