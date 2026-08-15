package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Player 玩家标记：UID（账号）挂到实体上。
// 随快照携带；加载存档时据此重建玩家所有权表（实体 → UID）。
type Player struct {
	UID string
}

type playerCodec struct{}

func (playerCodec) Encode(v Player) ([]byte, error) {
	return pb.Marshal(&game.Player{Uid: v.UID})
}

func (playerCodec) Decode(b []byte) (Player, error) {
	var p game.Player
	if err := pb.Unmarshal(b, &p); err != nil {
		return Player{}, err
	}
	return Player{UID: p.Uid}, nil
}

func RegisterPlayer(w *ecs.World) {
	ecs.RegisterComponent(w, "Player", playerCodec{})
}
