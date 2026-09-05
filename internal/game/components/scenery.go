package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Scenery 标记纯环境物的类型；实体只需再组合 Position，不附带交互、掉落或阻挡语义。
type Scenery struct {
	Kind ItemKind
}

type sceneryCodec struct{}

func (sceneryCodec) Encode(v Scenery) ([]byte, error) {
	return pb.Marshal(&game.Scenery{Kind: v.Kind})
}

func (sceneryCodec) Decode(data []byte) (Scenery, error) {
	var scenery game.Scenery
	if err := pb.Unmarshal(data, &scenery); err != nil {
		return Scenery{}, err
	}
	return Scenery{Kind: scenery.Kind}, nil
}

func RegisterScenery(w *ecs.World) {
	ecs.RegisterComponent(w, "Scenery", sceneryCodec{})
}
