package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// WorkstationType 工作站类型（单一事实来源 = proto 枚举）。
type WorkstationType = game.WorkstationType

const (
	StationCampfire  = game.WorkstationType_WORKSTATION_TYPE_CAMPFIRE
	StationWorkbench = game.WorkstationType_WORKSTATION_TYPE_WORKBENCH
)

// WorkstationTypeByName 配置字符串 → 工作站类型。
var WorkstationTypeByName = map[string]WorkstationType{
	"campfire":  StationCampfire,
	"workbench": StationWorkbench,
}

// Workstation 工作站标记：某些配方要求玩家附近有指定类型的工作站。
type Workstation struct {
	Type WorkstationType
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

func RegisterWorkstation(w *ecs.World) {
	ecs.RegisterComponent(w, "Workstation", workstationCodec{})
}
