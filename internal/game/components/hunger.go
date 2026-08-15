package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Hunger 饥饿值：每 tick 按速率扣减，归零后由 StarvationSystem 扣血。
// Rate 是每实体速率（不同角色可不同）；0 表示用世界默认。
type Hunger struct {
	Level int
	Rate  int
}

type hungerCodec struct{}

func (hungerCodec) Encode(v Hunger) ([]byte, error) {
	return pb.Marshal(&game.Hunger{Level: int32(v.Level), Rate: int32(v.Rate)})
}

func (hungerCodec) Decode(b []byte) (Hunger, error) {
	var h game.Hunger
	if err := pb.Unmarshal(b, &h); err != nil {
		return Hunger{}, err
	}
	return Hunger{Level: int(h.Level), Rate: int(h.Rate)}, nil
}

func RegisterHunger(w *ecs.World) {
	ecs.RegisterComponent(w, "Hunger", hungerCodec{})
}
