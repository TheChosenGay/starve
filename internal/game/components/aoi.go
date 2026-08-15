package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// AOI 感知组件：Radius = 感知半径（正方形，模板拷贝）；
// Visible = 本 tick 感知到的 liveable（派生数据，AOISystem 每 tick 重算，实体 id 升序）。
// Visible 是缓存：debug 模式才编码进快照；Radius 始终编码（存档/加载恢复感知范围）。
type AOI struct {
	Radius  int
	Visible []ecs.Entity
}

// aoiCodec 按 includeVisible（debug 开关）决定是否编码 Visible。
type aoiCodec struct{ includeVisible bool }

func (c aoiCodec) Encode(v AOI) ([]byte, error) {
	out := &game.AOI{Radius: int32(v.Radius)}
	if c.includeVisible {
		for _, e := range v.Visible {
			out.Visible = append(out.Visible, uint64(e))
		}
	}
	return pb.Marshal(out)
}

func (aoiCodec) Decode(b []byte) (AOI, error) {
	var m game.AOI
	if err := pb.Unmarshal(b, &m); err != nil {
		return AOI{}, err
	}
	out := AOI{Radius: int(m.Radius)}
	for _, id := range m.Visible {
		out.Visible = append(out.Visible, ecs.Entity(id))
	}
	return out, nil
}

// DebugFlags 调试开关（世界级 Resource）。
type DebugFlags struct {
	AOI bool // 调试：AOI.Visible 随快照下发（AOISystem 变更时 MarkDirty）
}

func RegisterAOI(w *ecs.World, debug bool) {
	ecs.RegisterComponent(w, "AOI", aoiCodec{includeVisible: debug})
}
