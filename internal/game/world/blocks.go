package world

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// rebuildBlocks 全量重建动态阻挡层：清空后按当前所有 Block 实体重写。
// 存档恢复后调用（恢复时组件按快照顺序挂载，Block 可能先于 Position，
// OnAdd 钩子看不到 Position 会跳过，这里做最终对账），保证 MapData 与实体一致。
func rebuildBlocks(sim *ecs.World) {
	md, ok := ecs.TryResource[MapData](sim)
	if !ok {
		return
	}
	md.ClearBlocked()
	ecs.Query2[components.Block, components.Position](sim, func(e ecs.Entity, b *components.Block, p *components.Position) {
		w, h := b.Width, b.Height
		if w <= 0 {
			w = 1
		}
		if h <= 0 {
			h = 1
		}
		md.SetBlockedRect(p.X, p.Y, w, h, true)
	})
}
