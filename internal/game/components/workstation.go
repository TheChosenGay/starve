package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Workstation 工作站标记：某些配方要求玩家附近有指定类型的工作站。
// 类型为配置驱动的字符串（如 "campfire"/"workbench"），加工作站类型不改代码。
type Workstation struct {
	Type string
}

type workstationCodec struct{}

func (workstationCodec) Encode(v Workstation) ([]byte, error) {
	return pb.Marshal(&game.Workstation{Type: v.Type})
}

func (workstationCodec) Decode(b []byte) (Workstation, error) {
	var w game.Workstation
	if err := pb.Unmarshal(b, &w); err != nil {
		return Workstation{}, err
	}
	return Workstation{Type: w.Type}, nil
}
