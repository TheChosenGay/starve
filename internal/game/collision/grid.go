package collision

import (
	"math"
	"slices"

	"starve/internal/ecs"
)

// DynamicGrid 是**动态体专用**的均匀网格（方案 B）。
//
// 与 AOIGrid 的关系：思路同源（按格分桶、两轮建立/查询），但为 ORCA 定制：
//   - 只登记**动态体**（有速度、参与互惠避让的），静态障碍不进网格——
//     它们走形状层的硬滑掠，与 ORCA 无关。这一点很关键：
//     AOI 的 O(r²) 标记成本无法避免，但动态体数量远少于全体实体，
//     且我们只需要登记**中心点所在格**，不需要按半径铺开。
//   - 查询是"取自己 r 范围内的格子"而不是"标记自己覆盖的格子"。
//     这是本方案与 AOI 最大的差别：**标记 O(1)（只写自己那一格），
//     查询 O(r²)（读自己周围 (2r+1)² 格）**。
//     AOI 是反过来的（标记 O(r²)、查询 O(1)）。
//
// 为什么这个方向更优：每 tick 每个实体**查询一次、标记一次**，两者都是 O(...)，
// 但标记只需要 1 格（实体只在自己的位置），而查询需要 r² 格。
// 所以总成本 = N×1（标记）+ N×r²（查询）。看起来还是 O(N·r²)，
// 但常数远小于 AOI 的"标记 r² + 查询 1"，因为：
//   - 标记写的是 1 个 append（AOI 是 r² 个）
//   - 查询遍历的格子虽然多，但每格通常只有 0~1 个实体（append 稀疏）
//
// 关键前提：**半径要小**。r=10 时查询 441 格；r=20 时 1681 格。
// 所以本方案适合小半径、且实体分布均匀的场景。
type DynamicGrid struct {
	// cellSize 是网格边长（格）。1 格最直观；大于 1 可以减少格子总数。
	cellSize float64
	cols     int
	rows     int
	// cells 是行优先的桶；每桶存该格内的动态体。
	cells [][]ecs.Entity
	// occupied 记录用过非空的桶下标（重置时只清这些，不遍历全图）。
	occupied []int
	// pos 缓存每个实体的位置（查询/判定都要用，避免重复读 ECS）。
	pos map[ecs.Entity][2]float64
	// cellOf 记录每个实体当前所在的桶下标（增量更新时用它定位旧桶）。
	cellOf map[ecs.Entity]int
}

// NewDynamicGrid 建一个覆盖 (0,0)-(w,h) 的网格。
func NewDynamicGrid(w, h int, cellSize float64) *DynamicGrid {
	if cellSize <= 0 {
		cellSize = 1
	}
	cols := int(math.Ceil(float64(w)/cellSize)) + 1
	rows := int(math.Ceil(float64(h)/cellSize)) + 1
	return &DynamicGrid{
		cellSize: cellSize,
		cols:     cols,
		rows:     rows,
		cells:    make([][]ecs.Entity, cols*rows),
		pos:      make(map[ecs.Entity][2]float64),
		cellOf:   make(map[ecs.Entity]int),
	}
}

// cellOf 返回坐标所属的格下标（越界返回 -1）。
func (g *DynamicGrid) cellIndex(x, z float64) int {
	cx := int(x / g.cellSize)
	cy := int(z / g.cellSize)
	if cx < 0 || cy < 0 || cx >= g.cols || cy >= g.rows {
		return -1
	}
	return cy*g.cols + cx
}

// Reset 清空网格（只清上轮用过的桶，复杂度与本轮实体数成正比，与地图大小无关）。
func (g *DynamicGrid) Reset() {
	for _, i := range g.occupied {
		g.cells[i] = g.cells[i][:0]
	}
	g.occupied = g.occupied[:0]
}

// Add 把一个动态体登记进它**所在的那一格**（O(1)）。
// 位置同时缓存下来，查询阶段不必再读 ECS。
//
// **增量更新**：实体已在网格里时，只有跨格才动桶；同格内移动只更新缓存位置
// （O(1)，不碰任何桶）。这让"每 tick 更新"变成"只有真的跨格才付出代价"——
// 玩家 10 格/秒、tick 50ms 时每 tick 走 0.5 格，平均 2 tick 才跨一次格。
func (g *DynamicGrid) Add(e ecs.Entity, x, z float64) {
	g.pos[e] = [2]float64{x, z}
	next := g.cellIndex(x, z)

	prev, existed := g.cellOf[e]
	if existed {
		if prev == next {
			return // 同格：什么都不用做
		}
		// 跨格：从旧桶摘掉（桶内元素少，线性扫可接受）
		if prev >= 0 {
			g.removeFromCell(prev, e)
		}
	}
	if next < 0 {
		if existed {
			delete(g.cellOf, e)
		}
		return
	}
	if len(g.cells[next]) == 0 {
		g.occupied = append(g.occupied, next)
	}
	g.cells[next] = append(g.cells[next], e)
	g.cellOf[e] = next
}

// removeFromCell 把实体从指定桶里摘掉（swap-remove，O(桶大小)）。
func (g *DynamicGrid) removeFromCell(cell int, e ecs.Entity) {
	bucket := g.cells[cell]
	for i, x := range bucket {
		if x == e {
			last := len(bucket) - 1
			bucket[i] = bucket[last]
			g.cells[cell] = bucket[:last]
			return
		}
	}
}

// Remove 把实体从网格里摘掉（实体死亡/移除时调用）。
func (g *DynamicGrid) Remove(e ecs.Entity) {
	if i, ok := g.cellOf[e]; ok {
		g.removeFromCell(i, e)
		delete(g.cellOf, e)
	}
	delete(g.pos, e)
}

// Pos 返回缓存的实体位置。
func (g *DynamicGrid) Pos(e ecs.Entity) (float64, float64, bool) {
	p, ok := g.pos[e]
	return p[0], p[1], ok
}

// Range 遍历 (x,z) 半径 r 内的候选实体（**只做格子级粗筛**，不做精确距离）。
// fn 返回 false 可提前终止。
//
// 注意：这是宽阶段——离得近的格子可能包住半径外的实体，调用方仍需算精确距离。
func (g *DynamicGrid) Range(x, z, r float64, fn func(e ecs.Entity) bool) {
	minCX := int((x - r) / g.cellSize)
	maxCX := int((x + r) / g.cellSize)
	minCY := int((z - r) / g.cellSize)
	maxCY := int((z + r) / g.cellSize)
	if minCX < 0 {
		minCX = 0
	}
	if minCY < 0 {
		minCY = 0
	}
	if maxCX >= g.cols {
		maxCX = g.cols - 1
	}
	if maxCY >= g.rows {
		maxCY = g.rows - 1
	}
	for cy := minCY; cy <= maxCY; cy++ {
		row := cy * g.cols
		for cx := minCX; cx <= maxCX; cx++ {
			for _, e := range g.cells[row+cx] {
				if !fn(e) {
					return
				}
			}
		}
	}
}

// maxNeighborRadius 是粗筛格子范围要预留的最大邻居半径（格）。
// 网格查询的格子级粗筛必须按 (r + 该值) 取范围，否则会漏掉
// "中心距略超 r、但两体表面仍相交"的邻居（见 Neighbors 注释）。
const maxNeighborRadius = 0.5

// Len 返回网格里的实体总数（观测用）。
func (g *DynamicGrid) Len() int { return len(g.pos) }

// Cols/Rows 返回网格尺寸（观测用）。
func (g *DynamicGrid) Cols() int { return g.cols }
func (g *DynamicGrid) Rows() int { return g.rows }

// NeighborsDynamicGrid 用网格收集邻居（方案 B 的实现）：
// 先粗筛格子、再算精确距离，结果按实体 id 排序保证确定性。
//
// exclude 是要排除的实体（自己）。
func (g *DynamicGrid) Neighbors(
	x, z, r float64, exclude ecs.Entity,
	radiusOf func(e ecs.Entity) float64,
	buf []Neighbor,
) []Neighbor {
	out := buf[:0]
	// 半径语义必须与 BVH 方案**完全一致**：基线的查询球半径是 r，
	// 而相交判定用的是 (r + 邻居半径)。所以粗筛格范围要按 (r + 最大邻居半径) 取，
	// 精确判定也要带邻居半径——否则会漏掉"中心距在 r 与 r+邻居半径之间"的那些，
	// 实测在 2000 实体密集布局下会少 8.8% 的邻居。
	maxR := r + maxNeighborRadius
	g.Range(x, z, maxR, func(e ecs.Entity) bool {
		if e == exclude {
			return true
		}
		ex, ez, ok := g.Pos(e)
		if !ok {
			return true
		}
		nr := radiusOf(e)
		reach := r + nr
		dx := ex - x
		dz := ez - z
		if dx*dx+dz*dz > reach*reach {
			return true // 格子级粗筛的漏网：真正的距离判定
		}
		out = append(out, Neighbor{Entity: e, X: ex, Z: ez, Radius: nr})
		return true
	})
	// 确定性：与 BVH 方案一样按实体 id 排序（ORCA 的增量 LP 依赖约束顺序）。
	slices.SortFunc(out, func(a, b Neighbor) int {
		switch {
		case a.Entity < b.Entity:
			return -1
		case a.Entity > b.Entity:
			return 1
		}
		return 0
	})
	return out
}
