package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Offline 离线标记：断线后实体保留在世界上（断线保留 N 分钟），
// 重连同 UID 且未死亡时复用该实体（清除标记）；超过保留时长由世界销毁。
type Offline struct {
	SinceTick int64 // 离线起始世界 tick
}

type offlineCodec struct{}

func (offlineCodec) Encode(v Offline) ([]byte, error) {
	return pb.Marshal(&game.Offline{SinceTick: v.SinceTick})
}

func (offlineCodec) Decode(b []byte) (Offline, error) {
	var o game.Offline
	if err := pb.Unmarshal(b, &o); err != nil {
		return Offline{}, err
	}
	return Offline{SinceTick: o.SinceTick}, nil
}

func RegisterOffline(w *ecs.World) {
	ecs.RegisterComponent(w, "Offline", offlineCodec{})
}
