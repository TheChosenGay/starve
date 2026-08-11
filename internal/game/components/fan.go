package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Fan 风扇：局部风力修正 + 下风向雾密度降低（实体组件）。
type Fan struct {
	Strength int // 风力（叠加到风速）
	DirX     int // 风向（-1/0/1，采样时归一化）
	DirY     int
	Radius   int // 作用半径
}

type fanCodec struct{}

func (fanCodec) Encode(v Fan) ([]byte, error) {
	return pb.Marshal(&game.Fan{Strength: int32(v.Strength), DirX: int32(v.DirX), DirY: int32(v.DirY), Radius: int32(v.Radius)})
}

func (fanCodec) Decode(b []byte) (Fan, error) {
	var m game.Fan
	if err := pb.Unmarshal(b, &m); err != nil {
		return Fan{}, err
	}
	return Fan{Strength: int(m.Strength), DirX: int(m.DirX), DirY: int(m.DirY), Radius: int(m.Radius)}, nil
}
