package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// MoveDir 一步移动方向（-1/0/1）；也是路径点（连续跟随的格子步进方向）。
type MoveDir struct {
	DX, DY int
}

// Moveable 可移动实体（玩家/生物）的连续速度移动状态：
//   - Speed：基础移动速度（格/秒）；
//   - EffectiveSpeed：效果修正后的权威速度（不含几何坡度；坡度在 MoveSystem 步进里乘）；
//   - DirX/DirY：输入方向（-1/0/1，客户端按住持续输入、松开清 0,0）；
//   - SubX/SubY：子格偏移 [0,1)，MoveSystem 每 tick 按 speed×dt 累积，跨格时提交到 Position；
//   - Path：待走路径点（空格自动行走 / AI 追击），非空时优先沿路径连续跟随，走完回落到输入方向。
//   - VelX/VelY：**实际速度**（格/秒，阶段③ ORCA 之后的产物），方向可任意（非整数）。
//
// 注意：**碰撞体不在这个组件里**——那是独立的 Collide 组件（形状 + 半径/胶囊），
// "是静态还是动态"由 Static/Dynamic 标记组件表达。Moveable 只描述"怎么动"。
//
// 只挂在可移动实体上；树/矿/掉落物等静态实体不带（Position 仍是纯坐标）。
type Moveable struct {
	Speed          float64   // 基础移动速度（格/秒）
	EffectiveSpeed float64   // 效果修正后的实际速度（格/秒）
	DirX, DirY     int       // 输入方向（-1/0/1；0,0 = 停止）
	SubX, SubY     float64   // 子格偏移（连续位移的亚格部分）
	Path           []MoveDir // 待走路径点（空 = 纯输入方向）
	// VelX/VelY 是**实际速度**（格/秒）：三阶段流水线的最终产物
	// （意图 → 静态滑掠 → ORCA 避让）。方向可任意，静止时为 (0,0)。
	// 位移累积用它的模长，而非 DirX/DirY 的整数方向。
	VelX, VelY float64
}

type moveableCodec struct{}

func (moveableCodec) Encode(v Moveable) ([]byte, error) {
	out := &game.Moveable{
		Speed:          v.Speed,
		EffectiveSpeed: v.EffectiveSpeed,
		DirX:           int32(v.DirX),
		DirY:           int32(v.DirY),
		SubX:           v.SubX,
		SubY:           v.SubY,
		VelX:           v.VelX,
		VelY:           v.VelY,
	}
	for _, d := range v.Path {
		out.Path = append(out.Path, &game.MoveDir{Dx: int32(d.DX), Dy: int32(d.DY)})
	}
	return pb.Marshal(out)
}

func (moveableCodec) Decode(b []byte) (Moveable, error) {
	var m game.Moveable
	if err := pb.Unmarshal(b, &m); err != nil {
		return Moveable{}, err
	}
	out := Moveable{
		Speed:          m.Speed,
		EffectiveSpeed: m.EffectiveSpeed,
		DirX:           int(m.DirX),
		DirY:           int(m.DirY),
		SubX:           m.SubX,
		SubY:           m.SubY,
		VelX:           m.VelX,
		VelY:           m.VelY,
	}
	for _, d := range m.Path {
		if d != nil {
			out.Path = append(out.Path, MoveDir{DX: int(d.Dx), DY: int(d.Dy)})
		}
	}
	return out, nil
}

func RegisterMoveable(w *ecs.World) {
	ecs.RegisterComponent(w, "Moveable", moveableCodec{})
}
