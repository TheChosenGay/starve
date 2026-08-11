package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// MoveDir 一步移动方向（-1/0/1）。
type MoveDir struct {
	DX, DY int
}

// Moveable 可移动实体（玩家）的 tick 制移动状态：
// move 命令是方向步进，进 Queue 缓存（顺序应用；0,0 停止命令清空队列）；
// MoveSystem 每 tick 按步进间隔消费队首一步。
// 只挂在可移动实体上；树/矿/掉落物等静态实体不带（Position 仍是纯坐标）。
type Moveable struct {
	Interval int       // 基础步进间隔（tick/格，如 2 = 每 2 tick 走一格）
	Elapsed  int       // 距上次移动经过的 tick 数
	Queue    []MoveDir // 待执行方向（有界，满了丢弃新指令）
}

type moveableCodec struct{}

func (moveableCodec) Encode(v Moveable) ([]byte, error) {
	out := &game.Moveable{Interval: int32(v.Interval), Elapsed: int32(v.Elapsed)}
	for _, d := range v.Queue {
		out.Queue = append(out.Queue, &game.MoveDir{Dx: int32(d.DX), Dy: int32(d.DY)})
	}
	return pb.Marshal(out)
}

func (moveableCodec) Decode(b []byte) (Moveable, error) {
	var m game.Moveable
	if err := pb.Unmarshal(b, &m); err != nil {
		return Moveable{}, err
	}
	out := Moveable{Interval: int(m.Interval), Elapsed: int(m.Elapsed)}
	for _, d := range m.Queue {
		if d != nil {
			out.Queue = append(out.Queue, MoveDir{DX: int(d.Dx), DY: int(d.Dy)})
		}
	}
	return out, nil
}
