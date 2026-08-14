package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// BlockTarget 阻挡写入目标：Block 的生命周期钩子通过它更新地图阻挡层。
// 用抽象接口而不是直接引用 worldmap.MapData——components ↔ worldmap 互相依赖会成环；
// MapData 天然实现该接口，世界侧通过 ecs.TryResourceOf 提供给钩子。
type BlockTarget interface {
	SetBlockedRect(x, y, w, h int, blocked bool)
}

// Block 阻挡组件：实体占据格子（有碰撞，参与移动/寻路阻挡）。
// 实现 ILifecycleAdd / ILifecycleRemove：挂载时写地图阻挡层、移除/实体销毁时清除，
// 由 ecs 内核自动触发，不需要调用方额外处理。
// Width×Height 为占格尺寸（缺省 1×1），锚点为 Position（左上角）。
// 未放置建筑（蓝图）不挂 Block；Place 成功时才挂。
type Block struct {
	Width, Height int
}

// OnAdd 挂载后：把占格写入地图阻挡层（需要 Position 已就位）。
func (b Block) OnAdd(w *ecs.World, e ecs.Entity) {
	blockTarget(w, e, true)
}

// OnRemove 移除前/实体销毁前：清除占格（此时 Position 与自身仍可读）。
func (b Block) OnRemove(w *ecs.World, e ecs.Entity) {
	blockTarget(w, e, false)
}

func blockTarget(w *ecs.World, e ecs.Entity, blocked bool) {
	if !ecs.Has[Position](w, e) {
		return
	}
	t, ok := ecs.TryResourceOf[BlockTarget](w)
	if !ok {
		return
	}
	b := ecs.Get[Block](w, e)
	ww, hh := b.Width, b.Height
	if ww <= 0 {
		ww = 1
	}
	if hh <= 0 {
		hh = 1
	}
	p := ecs.Get[Position](w, e)
	t.SetBlockedRect(p.X, p.Y, ww, hh, blocked)
}

type blockCodec struct{}

func (blockCodec) Encode(v Block) ([]byte, error) {
	return pb.Marshal(&game.Block{Width: int32(v.Width), Height: int32(v.Height)})
}

func (blockCodec) Decode(b []byte) (Block, error) {
	var m game.Block
	if err := pb.Unmarshal(b, &m); err != nil {
		return Block{}, err
	}
	return Block{Width: int(m.Width), Height: int(m.Height)}, nil
}
