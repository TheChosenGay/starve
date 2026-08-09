package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Respawn 重生标记：有该组件的环境物在耗尽后会自动恢复（倒计时单位 = tick）。
// 由 RespawnSystem 每 tick 递减，到点恢复 Workable.WorkLeft 并移除本组件。
type Respawn struct {
	Ticks int
}

type respawnCodec struct{}

func (respawnCodec) Encode(v Respawn) ([]byte, error) {
	return pb.Marshal(&game.Respawn{Ticks: int64(v.Ticks)})
}

func (respawnCodec) Decode(b []byte) (Respawn, error) {
	var r game.Respawn
	if err := pb.Unmarshal(b, &r); err != nil {
		return Respawn{}, err
	}
	return Respawn{Ticks: int(r.Ticks)}, nil
}
