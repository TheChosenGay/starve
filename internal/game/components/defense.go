package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Defense 防御属性（装备叠加到穿戴者）：Attackable 受击时读取并按此减免。
type Defense struct {
	Percent int
}

type defenseCodec struct{}

func (defenseCodec) Encode(v Defense) ([]byte, error) {
	return pb.Marshal(&game.Defense{Percent: int32(v.Percent)})
}

func (defenseCodec) Decode(b []byte) (Defense, error) {
	var m game.Defense
	if err := pb.Unmarshal(b, &m); err != nil {
		return Defense{}, err
	}
	return Defense{Percent: int(m.Percent)}, nil
}

func RegisterDefense(w *ecs.World) {
	ecs.RegisterComponent(w, "Defense", defenseCodec{})
}
