package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// CollideShape 是碰撞体的形状种类（显式声明，不再靠"半径>0"之类隐式约定推断）。
type CollideShape uint8

const (
	// CollideShapeNone 未指定（非法；写入前必须明确）。
	CollideShapeNone CollideShape = iota
	// CollideShapeCircle 格心圆：树/岩这类"占不满一格"的环境物。只占 1 格，用 Radius。
	CollideShapeCircle
	// CollideShapeBox 占格盒：建筑/工作站/城墙。用 Width×Height（格）。
	CollideShapeBox
	// CollideShapeCapsule 沿朝向铺开的胶囊：玩家/四足动物。用 Radius + HalfLength + 轴向。
	// HalfLength = 0 时退化成圆柱（直立人形）。
	CollideShapeCapsule
)

// String 便于日志/测试可读。
func (s CollideShape) String() string {
	switch s {
	case CollideShapeCircle:
		return "circle"
	case CollideShapeBox:
		return "box"
	case CollideShapeCapsule:
		return "capsule"
	default:
		return "none"
	}
}

// Collide 是实体的**碰撞体**组件：表示"我是障碍物，我的形状是这样"。
//
// 与 Block 的分工（两者正交，可只挂其一）：
//   - Collide：形状层。移动时扫掠 + 沿接触切面滑动，决定"能贴到多近"；
//   - Block：占位层。放置冲突（一格只归一个占位物）+ 寻路代价。
//
// 例如：树/岩/建筑两个都挂（既挡人也占格）；纯装饰物可能只挂 Block 不挡人，
// 或者只挂 Collide 挡人但不占格。
//
// 谁会动由 Static / Dynamic 标记决定：
//   - Static：形状固定，注册进索引后不再更新（fat AABB 余量 0）；
//   - Dynamic：每 tick 位置变化，需要按最新位置刷新形状（fat AABB 留余量）。
type Collide struct {
	Shape CollideShape // 形状种类（显式）
	// Radius：Circle 的半径 / Capsule 的截面半径（格）。
	Radius float64
	// Width/Height：Box 的占格尺寸（格）。
	Width, Height int
	// HalfLength：Capsule 沿朝向的半长（格）；0 = 直立圆柱（人物）。
	HalfLength float64
	// BodyHeight：碰撞体的**竖直**高度（格）。移动只用水平截面，这个字段供调试渲染用
	// （客户端按它把胶囊画成与模型等高）。注意与 Height（Box 的占格高）区分。
	BodyHeight float64
	// FaceX/FaceZ：Capsule 的轴向（-1/0/1，不要求归一化）；0,0 = 未指定 → 按 +X。
	// 由移动系统每 tick 从实际速度方向同步写入（静止时保留，避免身体突然转 90°）。
	FaceX, FaceZ int
}

type collideCodec struct{}

func (collideCodec) Encode(v Collide) ([]byte, error) {
	return pb.Marshal(&game.Collide{
		Shape:      game.CollideShape(v.Shape),
		Radius:     v.Radius,
		Width:      int32(v.Width),
		Height:     int32(v.Height),
		HalfLength: v.HalfLength,
		BodyHeight: v.BodyHeight,
		FaceX:      int32(v.FaceX),
		FaceZ:      int32(v.FaceZ),
	})
}

func (collideCodec) Decode(b []byte) (Collide, error) {
	var m game.Collide
	if err := pb.Unmarshal(b, &m); err != nil {
		return Collide{}, err
	}
	return Collide{
		Shape:      CollideShape(m.Shape),
		Radius:     m.Radius,
		Width:      int(m.Width),
		Height:     int(m.Height),
		HalfLength: m.HalfLength,
		BodyHeight: m.BodyHeight,
		FaceX:      int(m.FaceX),
		FaceZ:      int(m.FaceZ),
	}, nil
}

func RegisterCollide(w *ecs.World) {
	ecs.RegisterComponent(w, "Collide", collideCodec{})
}

// CollideTarget 碰撞体的世界侧写入目标：Collide 的生命周期钩子通过它维护形状层。
// 与 BlockerTarget 分开，正是因为"碰撞"和"占位"是两件独立的事。
type CollideTarget interface {
	// SetCollide 注册/更新一个实体的碰撞形状。
	SetCollide(w *ecs.World, e ecs.Entity, anchor Position, c Collide, dynamic bool)
	// ClearCollide 注销碰撞形状（未知实体忽略，幂等）。
	ClearCollide(w *ecs.World, e ecs.Entity)
}

// OnAdd 挂载后：把形状注册进碰撞索引（需要 Position 已就位）。
func (c Collide) OnAdd(w *ecs.World, e ecs.Entity) { collideTarget(w, e, true) }

// OnRemove 移除前/实体销毁前：注销形状。
func (c Collide) OnRemove(w *ecs.World, e ecs.Entity) { collideTarget(w, e, false) }

func collideTarget(w *ecs.World, e ecs.Entity, register bool) {
	t, ok := ecs.TryResourceOf[CollideTarget](w)
	if !ok {
		return
	}
	if !register {
		t.ClearCollide(w, e)
		return
	}
	if !ecs.Has[Position](w, e) {
		return // Position 后挂：由世界侧 rebuildCollides 兜底对账
	}
	c := ecs.Get[Collide](w, e)
	t.SetCollide(w, e, *ecs.Get[Position](w, e), *c, IsDynamic(w, e))
}
