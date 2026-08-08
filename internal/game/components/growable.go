package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Growable 可生长（树等）：每 N tick 长一阶段。
type Growable struct {
	Stage int
	Ticks int // 距上次生长经过的 tick 数
}

type growableCodec struct{}

func (growableCodec) Encode(v Growable) ([]byte, error) {
	return pb.Marshal(&game.Growable{Stage: int32(v.Stage), Ticks: int32(v.Ticks)})
}

func (growableCodec) Decode(b []byte) (Growable, error) {
	var g game.Growable
	if err := pb.Unmarshal(b, &g); err != nil {
		return Growable{}, err
	}
	return Growable{Stage: int(g.Stage), Ticks: int(g.Ticks)}, nil
}
