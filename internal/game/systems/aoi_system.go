package systems

import (
	"sort"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// AOIGrid 感知网格缓存（每世界一份 Resource）：每格记录覆盖它的 AOI 对象（实体 id）。
// touched 记录本轮被标记过的格子（可能重复，重置幂等）：下一轮开始前只清这些格子，
// 不遍历整张地图——复杂度与地图大小无关。
type AOIGrid struct {
	Width, Height int
	cells         [][]ecs.Entity
	touched       []int
	phase         int // 每 tick +1，用于降频判断
}

// AOISystem 感知结算（order 91，先于生物决策）：
// 第一轮：遍历 AOI 对象，按方形 radius 标记覆盖方块（Query 内一步完成）；
// 第二轮：遍历 liveable（Position+Health 且存活），按所在方块把身份写入覆盖者的 Visible。
// 确定性：Query 顺序 = dense 顺序（同路径重建一致）；live 再按实体 id 升序排序，
// 保证"实机 / 存档加载 / 重放"三条路径下 Visible 顺序一致（仇恨平局选目标不漂移）。
// AOISystem 感知结算（order 91）：按 Interval 降频刷新（默认每 4 tick 一次 ≈ 4Hz），
// 其余 tick 直接跳过——Visible 是缓存的感知结果，代价是感知最多滞后一个刷新周期。
// 复杂度 = O(N·r² + M·C)（N=感知者，M=liveable，C=单格覆盖数），与地图大小无关。
type AOISystem struct {
	Interval int // 刷新间隔（tick）；0 = 默认 4
}

// Update 实现 ECS 系统接口。
func (s *AOISystem) Update(w *ecs.World, dt time.Duration) {
	grid, ok := ecs.TryResource[AOIGrid](w)
	if !ok {
		return
	}
	interval := s.Interval
	if interval <= 0 {
		interval = 4
	}
	gw, gh := 128, 128 // 无地图兜底
	if md, ok := ecs.TryResource[worldmap.MapData](w); ok {
		gw, gh = md.Width, md.Height
	}
	s.ensureGrid(grid, gw, gh)
	grid.phase++
	if interval > 1 && grid.phase%interval != 1 {
		return // 本轮不重算：Visible 保持上次结果
	}
	grid.resetTouched()

	debug := false
	if df, ok := ecs.TryResource[components.DebugFlags](w); ok {
		debug = df.AOI
	}

	// 第一轮：重置 Visible + 标记覆盖（debug 时顺手 MarkDirty 推给客户端）
	ecs.Query[components.AOI](w, func(e ecs.Entity, aoi *components.AOI) {
		aoi.Visible = aoi.Visible[:0] // 重建本 tick 感知（复用底数组）
		if aoi.Radius <= 0 || !ecs.Has[components.Position](w, e) {
			if debug {
				ecs.MarkDirty[components.AOI](w, e)
			}
			return
		}
		p := ecs.Get[components.Position](w, e)
		for dy := -aoi.Radius; dy <= aoi.Radius; dy++ {
			for dx := -aoi.Radius; dx <= aoi.Radius; dx++ {
				x, y := p.X+dx, p.Y+dy
				if x < 0 || y < 0 || x >= gw || y >= gh {
					continue
				}
				grid.cells[y*gw+x] = append(grid.cells[y*gw+x], e)
				grid.touched = append(grid.touched, y*gw+x)
			}
		}
		if debug {
			ecs.MarkDirty[components.AOI](w, e)
		}
	})

	// 第二轮：liveable 加入覆盖者的 Visible
	var live []ecs.Entity
	ecs.Query2[components.Position, components.Health](w, func(e ecs.Entity, _ *components.Position, hp *components.Health) {
		if hp.Cur > 0 && w.IsAlive(e) && !ecs.Has[components.Dead](w, e) {
			live = append(live, e)
		}
	})
	sort.Slice(live, func(i, j int) bool { return live[i] < live[j] })
	for _, e := range live {
		p := ecs.Get[components.Position](w, e)
		if p.X < 0 || p.Y < 0 || p.X >= gw || p.Y >= gh {
			continue
		}
		for _, owner := range grid.cells[p.Y*gw+p.X] {
			if owner == e {
				continue // 不感知自己（遍历中 O(1) 比较即可，无需 set）
			}
			aoi := ecs.Get[components.AOI](w, owner)
			aoi.Visible = append(aoi.Visible, e)
		}
	}
}

func (s *AOISystem) ensureGrid(g *AOIGrid, w, h int) {
	if g.Width == w && g.Height == h && g.cells != nil {
		return
	}
	g.Width, g.Height = w, h
	g.cells = make([][]ecs.Entity, w*h)
}

// resetTouched 清空上一轮被标记过的格子（重复项幂等；不遍历整图）。
func (g *AOIGrid) resetTouched() {
	for _, i := range g.touched {
		g.cells[i] = g.cells[i][:0]
	}
	g.touched = g.touched[:0]
}
