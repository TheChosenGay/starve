package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Dead 死亡标记：实体死后保留在世界上（尸体/幽灵状态），
// 由后续系统处理（掉落、重生、清理）。快照会把它推给客户端。
type Dead struct {
	Reason    string
	SinceTick int64 // 死亡起始世界 tick（尸体清理用；0 由世界补盖）
}

type deadCodec struct{}

func (deadCodec) Encode(v Dead) ([]byte, error) {
	return pb.Marshal(&game.Dead{Reason: v.Reason, SinceTick: v.SinceTick})
}

func (deadCodec) Decode(b []byte) (Dead, error) {
	var d game.Dead
	if err := pb.Unmarshal(b, &d); err != nil {
		return Dead{}, err
	}
	return Dead{Reason: d.Reason, SinceTick: d.SinceTick}, nil
}

func RegisterDead(w *ecs.World) {
	ecs.RegisterComponent(w, "Dead", deadCodec{})
}
