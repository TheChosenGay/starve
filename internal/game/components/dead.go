package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Dead 死亡标记：实体死后保留在世界上（尸体/幽灵状态），
// 由后续系统处理（掉落、重生、清理）。快照会把它推给客户端。
type Dead struct {
	Reason string
}

type deadCodec struct{}

func (deadCodec) Encode(v Dead) ([]byte, error) {
	return pb.Marshal(&game.Dead{Reason: v.Reason})
}

func (deadCodec) Decode(b []byte) (Dead, error) {
	var d game.Dead
	if err := pb.Unmarshal(b, &d); err != nil {
		return Dead{}, err
	}
	return Dead{Reason: d.Reason}, nil
}
