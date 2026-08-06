package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// 组件 Codec：Go struct（无锁）⇄ proto message（跨端契约）。
// 快照/存档统一走这套编解码；客户端按组件名 + 本文件对应的 proto 解析。

type positionCodec struct{}

func (positionCodec) Encode(v Position) ([]byte, error) {
	return pb.Marshal(&game.Position{X: int32(v.X), Y: int32(v.Y)})
}

func (positionCodec) Decode(b []byte) (Position, error) {
	var p game.Position
	if err := pb.Unmarshal(b, &p); err != nil {
		return Position{}, err
	}
	return Position{X: int(p.X), Y: int(p.Y)}, nil
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

type hungerCodec struct{}

func (hungerCodec) Encode(v Hunger) ([]byte, error) {
	return pb.Marshal(&game.Hunger{Level: int32(v.Level)})
}

func (hungerCodec) Decode(b []byte) (Hunger, error) {
	var h game.Hunger
	if err := pb.Unmarshal(b, &h); err != nil {
		return Hunger{}, err
	}
	return Hunger{Level: int(h.Level)}, nil
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

// RegisterCodecs 为玩法组件注册名称 + codec（快照/存档用）。
// 必须在组件第一次被 Add/Query 之前调用（WorldActor 构造时执行）。
func RegisterCodecs(w *ecs.World) {
	ecs.RegisterComponent(w, "Position", positionCodec{})
	ecs.RegisterComponent(w, "Health", healthCodec{})
	ecs.RegisterComponent(w, "Hunger", hungerCodec{})
	ecs.RegisterComponent(w, "Growable", growableCodec{})
}
