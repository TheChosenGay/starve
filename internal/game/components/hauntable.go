package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

const (
	DefaultHauntUses     = 1
	DefaultHauntDuration = int64(60)
)

// Hauntable 是复活雕像的独立玩法状态。
type Hauntable struct {
	RemainingUses int
	DurationTicks int64
}

func (h Hauntable) Duration() int64 {
	if h.DurationTicks <= 0 {
		return DefaultHauntDuration
	}
	return h.DurationTicks
}

type hauntableCodec struct{}

func (hauntableCodec) Encode(v Hauntable) ([]byte, error) {
	return pb.Marshal(&game.Hauntable{
		RemainingUses: int32(v.RemainingUses),
		DurationTicks: v.DurationTicks,
	})
}

func (hauntableCodec) Decode(b []byte) (Hauntable, error) {
	var m game.Hauntable
	if err := pb.Unmarshal(b, &m); err != nil {
		return Hauntable{}, err
	}
	return Hauntable{
		RemainingUses: int(m.RemainingUses),
		DurationTicks: m.DurationTicks,
	}, nil
}

func RegisterHauntable(w *ecs.World) {
	ecs.RegisterComponent(w, "Hauntable", hauntableCodec{})
}
