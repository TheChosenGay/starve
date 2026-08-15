package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// WorkAction 可交互动作（单一事实来源 = proto 枚举）。
type WorkAction = game.WorkAction

const (
	WorkChop = game.WorkAction_WORK_ACTION_CHOP
	WorkMine = game.WorkAction_WORK_ACTION_MINE
	WorkPick = game.WorkAction_WORK_ACTION_PICK
)

// WorkActionByName 配置字符串 → 工作动作。
var WorkActionByName = map[string]WorkAction{
	"chop": WorkChop,
	"mine": WorkMine,
	"pick": WorkPick,
}

// Workable 可交互环境物（树/矿/浆果丛）：动作 + 剩余工作量。
// 耐久用 WorkLeft 表达（不走 Combat Health），生物才用 Health。
type Workable struct {
	Kind     ItemKind
	Action   WorkAction
	WorkLeft int
	MaxWork  int
}

type workableCodec struct{}

func (workableCodec) Encode(v Workable) ([]byte, error) {
	return pb.Marshal(&game.Workable{
		Kind:     v.Kind,
		Action:   v.Action,
		WorkLeft: int32(v.WorkLeft),
		MaxWork:  int32(v.MaxWork),
	})
}

func (workableCodec) Decode(b []byte) (Workable, error) {
	var w game.Workable
	if err := pb.Unmarshal(b, &w); err != nil {
		return Workable{}, err
	}
	return Workable{Kind: w.Kind, Action: w.Action, WorkLeft: int(w.WorkLeft), MaxWork: int(w.MaxWork)}, nil
}

func RegisterWorkable(w *ecs.World) {
	ecs.RegisterComponent(w, "Workable", workableCodec{})
}
