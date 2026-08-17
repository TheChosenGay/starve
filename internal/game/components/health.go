package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Health 生命值（纯数据）。伤害减免由 Attackable 依赖的 Defense 处理，Health 本身不依赖防御。
type Health struct {
	Cur int
	Max int
}

// TakeDamage 直接扣血（clamp 到 0），返回实际扣血量。减免在调用方（Attackable）完成。
func (h *Health) TakeDamage(attack int) int {
	dealt := attack
	if dealt > h.Cur {
		dealt = h.Cur
	}
	if dealt < 0 {
		dealt = 0
	}
	h.Cur -= dealt
	return dealt
}

type healthCodec struct{}

func (healthCodec) Encode(v Health) ([]byte, error) {
	return pb.Marshal(&game.Health{Cur: int32(v.Cur), Max: int32(v.Max)})
}

func (healthCodec) Decode(b []byte) (Health, error) {
	var h game.Health
	if err := pb.Unmarshal(b, &h); err != nil {
		return Health{}, err
	}
	return Health{Cur: int(h.Cur), Max: int(h.Max)}, nil
}

func RegisterHealth(w *ecs.World) {
	ecs.RegisterComponent(w, "Health", healthCodec{})
}
