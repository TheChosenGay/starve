package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// BlockerTarget 占位物的世界侧写入目标：占位组件的生命周期钩子通过它同时维护两个投影——
//   - 形状碰撞层（collision.World）：走路时扫掠 + 沿接触切面滑动，决定"能贴到多近"；
//   - 占位层（MapData.Occupied）：放置冲突（一格只归一个占位物）+ 寻路代价（绕开）。
//
// 用抽象接口而不是直接引用世界侧类型——components ↔ world 互相依赖会成环。
type BlockerTarget interface {
	// SetBlocker 注册/更新一个占位物。anchor 是锚点格（左上角），width×height 是占格尺寸；
	// circleRadius > 0 时形状改为以格心为轴的圆柱（半径，占 1 格）。
	SetBlocker(w *ecs.World, e ecs.Entity, anchor Position, width, height int, circleRadius float64)
	// ClearBlocker 注销占位物（未知实体忽略，幂等）。
	ClearBlocker(w *ecs.World, e ecs.Entity)
}

// Block 占位组件：实体占住格子，并自带形状碰撞体。
//
// 语义（占位 ≠ 不可走，三层各答一个问题）：
//   - 地形层 MapData.Walkable：水/悬崖才不可走，占位格照样能走进去；
//   - 占位层 MapData.Occupied：放置冲突（占位格不能再放别的占位物）+ 寻路代价
//     （穿过占位格加 OccupiedCostThin / OccupiedCostFull，所以 A* 会绕开，
//     但被占位物围死时仍能算出路径，而不是直接判不可达）；
//   - 形状层 collision.World：圆的半径 / 盒的占格矩形决定角色能贴到多近，
//     撞上之后沿接触切面滑开（不再有"离一整格就停下"的手感）。
//
// 两种形状互斥（看 Radius）：
//   - Radius > 0：格心圆柱，占 1 格。树/岩这类"占不满一格"的环境物。
//   - Radius = 0：占格矩形盒 Width×Height。建筑/工作站/城墙这类整格物。
//
// 实现 ILifecycleAdd / ILifecycleRemove：挂载时注册、移除/实体销毁时注销，
// 由 ecs 内核自动触发，不需要调用方额外处理。未放置建筑（蓝图）不挂 Block。
type Block struct {
	Radius        float64 // > 0：格心圆柱半径（格），占 1 格；0：整格矩形盒
	Width, Height int     // 盒占格尺寸（Radius > 0 时按 1×1 处理）
}

// OnAdd 挂载后：注册形状碰撞体与占位（需要 Position 已就位）。
func (b Block) OnAdd(w *ecs.World, e ecs.Entity) {
	blockerTarget(w, e, true)
}

// OnRemove 移除前/实体销毁前：注销形状碰撞体与占位（此时 Position 与自身仍可读）。
func (b Block) OnRemove(w *ecs.World, e ecs.Entity) {
	blockerTarget(w, e, false)
}

func blockerTarget(w *ecs.World, e ecs.Entity, register bool) {
	t, ok := ecs.TryResourceOf[BlockerTarget](w)
	if !ok {
		return
	}
	if !register {
		t.ClearBlocker(w, e)
		return
	}
	if !ecs.Has[Position](w, e) {
		return // Position 后挂：由世界侧 rebuildBlockers 兜底对账
	}
	b := ecs.Get[Block](w, e)
	t.SetBlocker(w, e, *ecs.Get[Position](w, e), b.Width, b.Height, b.Radius)
}

type blockCodec struct{}

func (blockCodec) Encode(v Block) ([]byte, error) {
	return pb.Marshal(&game.Block{
		Width:  int32(v.Width),
		Height: int32(v.Height),
		Radius: v.Radius,
	})
}

func (blockCodec) Decode(b []byte) (Block, error) {
	var m game.Block
	if err := pb.Unmarshal(b, &m); err != nil {
		return Block{}, err
	}
	return Block{Width: int(m.Width), Height: int(m.Height), Radius: m.Radius}, nil
}

func RegisterBlock(w *ecs.World) {
	ecs.RegisterComponent(w, "Block", blockCodec{})
}
