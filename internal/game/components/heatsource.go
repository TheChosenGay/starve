package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// HeatSource 热源（火堆等）：局部升温 + 降雾（实体组件）。
type HeatSource struct {
	Strength int // 升温量
	Radius   int // 作用半径
}

type heatSourceCodec struct{}

func (heatSourceCodec) Encode(v HeatSource) ([]byte, error) {
	return pb.Marshal(&game.HeatSource{Strength: int32(v.Strength), Radius: int32(v.Radius)})
}

func (heatSourceCodec) Decode(b []byte) (HeatSource, error) {
	var m game.HeatSource
	if err := pb.Unmarshal(b, &m); err != nil {
		return HeatSource{}, err
	}
	return HeatSource{Strength: int(m.Strength), Radius: int(m.Radius)}, nil
}
