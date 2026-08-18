package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Attackable 受击能力（-able）：被作用方接受 Attacker 的攻击。
// 对 Defense 和 Health 有依赖：先按 Defense 减免，再把伤害应用到 Health，
// 并触发受击反馈（AI 标记/仇恨/打断）。
type Attackable struct{}

// Usable 目标是否还能被攻击：存活 + 有血 + 未标记死亡。
func (Attackable) Usable(w *ecs.World, e ecs.Entity) bool {
	return w.IsAlive(e) && ecs.Has[Health](w, e) &&
		ecs.Get[Health](w, e).Cur > 0 && !ecs.Has[Dead](w, e)
}

// ApplyDamage 受击结算：按防御减免 → 应用到 Health → 受击反馈（组件内部处理）。
// 防御不复制到穿戴者身上：受击时从 Equip 头/身槽位的护甲实体 Defense 求和（未穿戴 = 0）。
func (Attackable) ApplyDamage(w *ecs.World, target, attacker ecs.Entity, damage int) {
	if damage <= 0 || !w.IsAlive(target) || ecs.Has[Dead](w, target) || !ecs.Has[Health](w, target) {
		return
	}
	if d := defensePercent(w, target); d > 0 {
		damage = damage * (100 - d) / 100
		if damage < 0 {
			damage = 0
		}
	}
	hp := ecs.Get[Health](w, target)
	dealt := hp.TakeDamage(damage)
	ecs.MarkDirty[Health](w, target)

	// 受击标记（AI 输入：本 tick 被谁打）
	if ecs.Has[AI](w, target) {
		ecs.Get[AI](w, target).MarkAttacked(w, target, attacker)
	}
	// 仇恨（被打的生物记仇，按实际伤害）
	if ecs.Has[Creature](w, target) {
		ecs.Get[Creature](w, target).AddThreat(w, target, attacker, int32(dealt))
	}
	// 受击打断（制作等）
	TryInterrupt(w, target)
}

// defensePercent 目标当前防御减免：头/身槽位装备实体的 Defense 之和（组件只认装备，穿戴者不存防御）。
func defensePercent(w *ecs.World, e ecs.Entity) int {
	if !ecs.Has[Equip](w, e) {
		return 0
	}
	eq := ecs.Get[Equip](w, e)
	total := 0
	for _, slot := range []Slot{SlotHead, SlotBody} {
		if item := eq.Item(slot); item != 0 && ecs.Has[Defense](w, item) {
			total += ecs.Get[Defense](w, item).Percent
		}
	}
	return total
}

type attackableCodec struct{}

func (attackableCodec) Encode(v Attackable) ([]byte, error) {
	return pb.Marshal(&game.Attackable{})
}

func (attackableCodec) Decode(b []byte) (Attackable, error) {
	var m game.Attackable
	if err := pb.Unmarshal(b, &m); err != nil {
		return Attackable{}, err
	}
	return Attackable{}, nil
}

func RegisterAttackable(w *ecs.World) {
	ecs.RegisterComponent(w, "Attackable", attackableCodec{})
}
