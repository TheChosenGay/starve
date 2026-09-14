package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// DebugShape 调试用简化碰撞体：世界开调试开关时由世界层写给实体，随快照下发，
// 客户端把它画成线框/半透明体，用来肉眼核对"碰撞体是不是刚好包住渲染模型"。
//
// 坐标是**实体节点局部空间**（单位=格，y=0 在节点原点）：
//   - 客户端按实体的渲染规则给出节点的世界位置与朝向（占位物 = 占格中心、不转向；
//     移动体 = 连续位置 + 朝向 yaw），形状再相对节点表达，两端不重复施加变换；
//   - 胶囊段沿**模型局部 +Z**（客户端把局部 +Z 对准朝向，见客户端 IsoCamera3D.FacingYaw），
//     服务端**不预先旋转**——预先转到世界朝向会被客户端再转一次，误差恰好等于朝向角；
//   - 盒以节点为中心：(0,0,0) 是盒中心，尺寸取 Width×Height×Depth。
//
// 关掉调试开关时组件会被摘掉（下发 stopped/removed），客户端随之隐藏。
type DebugShape struct {
	Kind   DebugShapeKind
	Radius float64 // 胶囊半径（格）
	AX     float64 // 胶囊段起点（局部空间）
	AY     float64
	AZ     float64
	BX     float64 // 胶囊段终点（局部空间）
	BY     float64
	BZ     float64
	Width  float64 // 盒：占格宽（以节点为中心）
	Depth  float64 // 盒：占格深（以节点为中心）
	Height float64 // 盒高 / 胶囊竖直跨度（调试渲染用）
	Source string  // 形状来源（模型路径/配置名），调试面板可显示
}

// DebugShapeKind 调试形状类型。
type DebugShapeKind int

const (
	DebugShapeCapsule DebugShapeKind = iota + 1
	DebugShapeBox
)

type debugShapeCodec struct{}

func (debugShapeCodec) Encode(v DebugShape) ([]byte, error) {
	kind := game.DebugShape_DEBUG_SHAPE_KIND_CAPSULE
	if v.Kind == DebugShapeBox {
		kind = game.DebugShape_DEBUG_SHAPE_KIND_BOX
	}
	return pb.Marshal(&game.DebugShape{
		Kind:   kind,
		Radius: v.Radius,
		AX:     v.AX, AY: v.AY, AZ: v.AZ,
		BX: v.BX, BY: v.BY, BZ: v.BZ,
		Width: v.Width, Depth: v.Depth, Height: v.Height,
		Source: v.Source,
	})
}

func (debugShapeCodec) Decode(b []byte) (DebugShape, error) {
	var m game.DebugShape
	if err := pb.Unmarshal(b, &m); err != nil {
		return DebugShape{}, err
	}
	kind := DebugShapeCapsule
	if m.Kind == game.DebugShape_DEBUG_SHAPE_KIND_BOX {
		kind = DebugShapeBox
	}
	return DebugShape{
		Kind:   kind,
		Radius: m.Radius,
		AX:     m.AX, AY: m.AY, AZ: m.AZ,
		BX: m.BX, BY: m.BY, BZ: m.BZ,
		Width: m.Width, Depth: m.Depth, Height: m.Height,
		Source: m.Source,
	}, nil
}

func RegisterDebugShape(w *ecs.World) {
	ecs.RegisterComponent(w, "DebugShape", debugShapeCodec{})
}
