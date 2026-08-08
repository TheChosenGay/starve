package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Health 生命值（纯数据，无锁）。
type Health struct {
	Cur int
	Max int
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
