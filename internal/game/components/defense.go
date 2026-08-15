package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Defense 防御属性：实现组件生命周期，挂到实体上时自动叠加 Health.DefensePercent，
// 移除时自动扣除——装备流程只需 Add/Remove 本组件，效果由组件自身闭环。
type Defense struct {
	Percent int
}

// OnAdd 挂载后：实体有 Health 则叠加防御。
func (d Defense) OnAdd(w *ecs.World, e ecs.Entity) {
	if !ecs.Has[Health](w, e) {
		return
	}
	hp := ecs.Get[Health](w, e)
	hp.DefensePercent += d.Percent
	ecs.MarkDirty[Health](w, e)
}

// OnRemove 移除前：实体有 Health 则扣减防御。
func (d Defense) OnRemove(w *ecs.World, e ecs.Entity) {
	if !ecs.Has[Health](w, e) {
		return
	}
	hp := ecs.Get[Health](w, e)
	hp.DefensePercent -= d.Percent
	if hp.DefensePercent < 0 {
		hp.DefensePercent = 0
	}
	ecs.MarkDirty[Health](w, e)
}

type defenseCodec struct{}

func (defenseCodec) Encode(v Defense) ([]byte, error) {
	return pb.Marshal(&game.Defense{Percent: int32(v.Percent)})
}

func (defenseCodec) Decode(b []byte) (Defense, error) {
	var m game.Defense
	if err := pb.Unmarshal(b, &m); err != nil {
		return Defense{}, err
	}
	return Defense{Percent: int(m.Percent)}, nil
}

func RegisterDefense(w *ecs.World) {
	ecs.RegisterComponent(w, "Defense", defenseCodec{})
}
