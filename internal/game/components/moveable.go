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
//   - BodyRadius/BodyHalfLength：实体碰撞体（由客户端模型推导，见 docs/模型到碰撞体流水线.md）。
//     BodyHalfLength > 0 是沿朝向铺开的胶囊（四足：长宽刚好包住模型），= 0 是圆柱（人物）；
//     BodyRadius = 0 时回退到全局 systems.BodyRadius。
//   - FacingX/FacingY：最近一次移动方向（胶囊轴向；静止时保留，避免身体突然转 90°）。
//
// 只挂在可移动实体上；树/矿/掉落物等静态实体不带（Position 仍是纯坐标）。
type Moveable struct {
	Speed            float64   // 基础移动速度（格/秒）
	EffectiveSpeed   float64   // 效果修正后的实际速度（格/秒）
	DirX, DirY       int       // 输入方向（-1/0/1；0,0 = 停止）
	SubX, SubY       float64   // 子格偏移（连续位移的亚格部分）
	Path             []MoveDir // 待走路径点（空 = 纯输入方向）
	BodyRadius       float64   // 碰撞体半径（格；0 = 用全局默认）
	BodyHeight       float64   // 碰撞体高（格；仅调试渲染/契约用，移动只用水平截面）
	BodyHalfLength   float64   // 胶囊半长（格；0 = 圆柱）
	FacingX, FacingY int       // 轴向（-1/0/1；0,0 = 未移动过）
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
		BodyRadius:     v.BodyRadius,
		BodyHeight:     v.BodyHeight,
		BodyHalfLength: v.BodyHalfLength,
		FacingX:        int32(v.FacingX),
		FacingY:        int32(v.FacingY),
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
		BodyRadius:     m.BodyRadius,
		BodyHeight:     m.BodyHeight,
		BodyHalfLength: m.BodyHalfLength,
		FacingX:        int(m.FacingX),
		FacingY:        int(m.FacingY),
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
