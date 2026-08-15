package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Respawnable 可重生：创建时挂载（seed），倒计时由 RespawnSystem 闭环管理。
// 与交互能力（Choppable/Minable/Pickable）解耦，是独立的重生机制。
type Respawnable struct {
	Ticks     int // 重生间隔（tick）
	TicksLeft int // 剩余倒计时；0 = 未在计数
}

type respawnableCodec struct{}

func (respawnableCodec) Encode(v Respawnable) ([]byte, error) {
	return pb.Marshal(&game.Respawnable{Ticks: int32(v.Ticks), TicksLeft: int32(v.TicksLeft)})
}

func (respawnableCodec) Decode(b []byte) (Respawnable, error) {
	var m game.Respawnable
	if err := pb.Unmarshal(b, &m); err != nil {
		return Respawnable{}, err
	}
	return Respawnable{Ticks: int(m.Ticks), TicksLeft: int(m.TicksLeft)}, nil
}

func RegisterRespawnable(w *ecs.World) {
	ecs.RegisterComponent(w, "Respawnable", respawnableCodec{})
}
