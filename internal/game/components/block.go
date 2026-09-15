package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// BlockerTarget 占位物的世界侧写入目标：占位组件的生命周期钩子通过它维护占位层——
// MapData.Occupied：放置冲突（一格只归一个占位物）+ 寻路代价（绕开）。
//
// 注意：**碰撞形状不走这里**——那是 Collide 组件自己负责的（见 collide.go）。
// 用抽象接口而不是直接引用世界侧类型——components ↔ world 互相依赖会成环。
type BlockerTarget interface {
	// SetBlocker 登记/更新一个占位物。anchor 是锚点格（左上角），width×height 是占格尺寸。
	SetBlocker(w *ecs.World, e ecs.Entity, anchor Position, width, height int, thin bool)
	// ClearBlocker 注销占位（未知实体忽略，幂等）。
	ClearBlocker(w *ecs.World, e ecs.Entity)
}

// Block 占位组件：实体占住格子（放置冲突 + 寻路代价）。
//
// 语义（占位 ≠ 不可走，三层各答一个问题）：
//   - 地形层 MapData.Walkable：水/悬崖才不可走，占位格照样能走进去；
//   - 占位层 MapData.Occupied：放置冲突（占位格不能再放别的占位物）+ 寻路代价
//     （穿过占位格加 OccupiedCostThin / OccupiedCostFull，所以 A* 会绕开，
//     但被占位物围死时仍能算出路径，而不是直接判不可达）；
//   - 形状层 collision.Index：**由独立的 Collide 组件提供**，决定角色能贴到多近。
//
// 与 Collide 正交：Block 只回答"这一格归谁"，不回答"能贴到多近"。两者可只挂其一。
//
// 实现 ILifecycleAdd / ILifecycleRemove：挂载时登记、移除/实体销毁时注销，
// 由 ecs 内核自动触发。未放置建筑（蓝图）不挂 Block。
type Block struct {
	Width, Height int // 占格尺寸（格）
	// Thin 表示"占不满一格"的软占位（树/岩的格心圆）：
	// 走直线撞上树干要贴着滑，慢且别扭，所以给**低**寻路代价——绕开只要多走几格，
	// A* 就会选择绕开；被围住时仍然可达（贵，但不是墙）。
	// Thin=false 是整格硬占位（建筑/工作站/城墙）：代价给到"实际不会选"，
	// 但仍是有限值，被围墙封死时 A* 依然给得出路径。
	Thin bool
}

// OnAdd 挂载后：登记占位（需要 Position 已就位）。
func (b Block) OnAdd(w *ecs.World, e ecs.Entity) {
	blockerTarget(w, e, true)
}

// OnRemove 移除前/实体销毁前：注销占位（此时 Position 与自身仍可读）。
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
	t.SetBlocker(w, e, *ecs.Get[Position](w, e), b.Width, b.Height, b.Thin)
}

type blockCodec struct{}

func (blockCodec) Encode(v Block) ([]byte, error) {
	return pb.Marshal(&game.Block{
		Width:  int32(v.Width),
		Height: int32(v.Height),
		Thin:   v.Thin,
	})
}

func (blockCodec) Decode(b []byte) (Block, error) {
	var m game.Block
	if err := pb.Unmarshal(b, &m); err != nil {
		return Block{}, err
	}
	return Block{Width: int(m.Width), Height: int(m.Height), Thin: m.Thin}, nil
}

func RegisterBlock(w *ecs.World) {
	ecs.RegisterComponent(w, "Block", blockCodec{})
}
