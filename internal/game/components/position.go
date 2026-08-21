package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Position 是世界坐标（模拟层数据，服务器权威）。
// 端上只消费它做渲染，不参与计算。
type Position struct {
	X, Y int
}

// Manhattan 曼哈顿距离（网格移动/攻击范围判定用）。
func (p Position) Manhattan(q Position) int {
	dx := p.X - q.X
	if dx < 0 {
		dx = -dx
	}
	dy := p.Y - q.Y
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

// WithinRange 判断与另一个位置的距离是否在 r 内。
func (p Position) WithinRange(q Position, r int) bool {
	return p.Manhattan(q) <= r
}

// ManhattanToFootprint 返回到以 anchor 为左上角的矩形占格的最短曼哈顿距离。
// 非法尺寸按 1×1 处理，兼容旧快照中缺失的占格尺寸。
func (p Position) ManhattanToFootprint(anchor Position, width, height int) int {
	if width <= 0 {
		width = 1
	}
	if height <= 0 {
		height = 1
	}
	dx := axisDistanceToRange(p.X, anchor.X, anchor.X+width-1)
	dy := axisDistanceToRange(p.Y, anchor.Y, anchor.Y+height-1)
	return dx + dy
}

func axisDistanceToRange(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue - value
	}
	if value > maxValue {
		return value - maxValue
	}
	return 0
}

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

func RegisterPosition(w *ecs.World) {
	ecs.RegisterComponent(w, "Position", positionCodec{})
}
