package worldmap

import (
	"container/heap"

	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// WalkGrid 寻路专用可走性网格（与 MapData 解耦）：
// 静态地形在构建时初始化（非水可走），动态障碍（固定建筑等）由外部 SetBlocked 增量更新；
// 路径算法只读它，不直接依赖地图表示。
type WalkGrid struct {
	Width, Height int
	walkable      []byte // 1=可走 0=不可走（行优先）
}

// NewWalkGrid 从地形（非水）构建寻路网格。
func NewWalkGrid(md *MapData) *WalkGrid {
	g := EmptyWalkGrid(0, 0)
	g.Rebuild(md)
	return g
}

// EmptyWalkGrid 空网格（w/h ≤ 0 时先占位，等 Rebuild 定型）。
func EmptyWalkGrid(w, h int) *WalkGrid {
	if w <= 0 || h <= 0 {
		return &WalkGrid{}
	}
	g := &WalkGrid{Width: w, Height: h, walkable: make([]byte, w*h)}
	for i := range g.walkable {
		g.walkable[i] = 1
	}
	return g
}

// Rebuild 从地形重建（世界构建/存档加载时调用，O(WH) 一次性，不是每 tick）。
func (g *WalkGrid) Rebuild(md *MapData) {
	if md == nil || md.Width <= 0 || md.Height <= 0 {
		return
	}
	if g.Width != md.Width || g.Height != md.Height || len(g.walkable) != md.Width*md.Height {
		g.Width, g.Height = md.Width, md.Height
		g.walkable = make([]byte, md.Width*md.Height)
	}
	for y := 0; y < md.Height; y++ {
		for x := 0; x < md.Width; x++ {
			if terrainWalkable(md, x, y) {
				g.walkable[y*md.Width+x] = 1
			} else {
				g.walkable[y*md.Width+x] = 0
			}
		}
	}
}

// Walkable 该格是否可走（越界 = 不可走）。
func (g *WalkGrid) Walkable(x, y int) bool {
	if g == nil || x < 0 || y < 0 || x >= g.Width || y >= g.Height {
		return false
	}
	return g.walkable[y*g.Width+x] == 1
}

// SetBlocked 增量更新一格可走性（建筑创建/销毁时调用，O(1)）。
func (g *WalkGrid) SetBlocked(x, y int, blocked bool) {
	if g == nil || x < 0 || y < 0 || x >= g.Width || y >= g.Height {
		return
	}
	if blocked {
		g.walkable[y*g.Width+x] = 0
	} else {
		g.walkable[y*g.Width+x] = 1
	}
}

// SetBlockedRect 批量设置一个区域（左上角 + 宽高）的可走性（建筑占格/拆除用）。
func (g *WalkGrid) SetBlockedRect(x, y, w, h int, blocked bool) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			g.SetBlocked(x+dx, y+dy, blocked)
		}
	}
}

// AllWalkable 批量判断一个区域（左上角 + 宽高）是否全部可走（建筑放置校验用）。
func (g *WalkGrid) AllWalkable(x, y, w, h int) bool {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			if !g.Walkable(x+dx, y+dy) {
				return false
			}
		}
	}
	return true
}

// terrainWalkable 地形层可走 = 非水（用角地形判断）。
func terrainWalkable(md *MapData, x, y int) bool {
	if len(md.CornerTypes) == (md.Width+1)*(md.Height+1) {
		return game.TerrainType(md.CornerTypes[y*(md.Width+1)+x]) != game.TerrainType_TERRAIN_TYPE_WATER
	}
	return true
}

// FindPath A* 网格寻路（曼哈顿启发式 + goal-biased tie-break + 稀疏 visited map）：
// 空旷地形下探索量 ~O(距离)、分配 ~O(路径长度)，长路径/大图显著优于 BFS；
// 确定性：堆按 (f, g 降序, 格索引) 平局（f 相同优先更深，朝目标方向拉直）。
func FindPath(g *WalkGrid, fromX, fromY, toX, toY int) []components.MoveDir {
	if g == nil || !g.Walkable(fromX, fromY) || !g.Walkable(toX, toY) {
		return nil
	}
	w := g.Width
	start, goal := fromY*w+fromX, toY*w+toX
	if start == goal {
		return nil
	}
	gScore := map[int]int{start: 0}
	cameFrom := map[int]int{}
	closed := map[int]bool{}
	open := &astarHeap{{f: manhattan(fromX, fromY, toX, toY), g: 0, idx: start}}
	heap.Init(open)
	for open.Len() > 0 {
		cur := heap.Pop(open).(astarNode)
		if cur.idx == goal {
			break
		}
		if closed[cur.idx] {
			continue
		}
		closed[cur.idx] = true
		cx, cy := cur.idx%w, cur.idx/w
		for _, d := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := cx+d[0], cy+d[1]
			if !g.Walkable(nx, ny) {
				continue
			}
			n := ny*w + nx
			ng := gScore[cur.idx] + 1
			if old, ok := gScore[n]; ok && old <= ng {
				continue
			}
			gScore[n] = ng
			cameFrom[n] = cur.idx
			heap.Push(open, astarNode{f: ng + manhattan(nx, ny, toX, toY), g: ng, idx: n})
		}
	}
	goalParent, ok := cameFrom[goal]
	if !ok {
		return nil
	}
	_ = goalParent
	return backtrackFrom(cameFrom, w, start, goal)
}

// backtrackFrom 从 A* cameFrom map 回溯方向序列（不含起点）。
func backtrackFrom(cameFrom map[int]int, w, start, goal int) []components.MoveDir {
	var rev []components.MoveDir
	cur := goal
	for cur != start {
		p, ok := cameFrom[cur]
		if !ok {
			return nil
		}
		px, py := p%w, p/w
		cx, cy := cur%w, cur/w
		rev = append(rev, components.MoveDir{DX: cx - px, DY: cy - py})
		cur = p
	}
	out := make([]components.MoveDir, len(rev))
	for i, d := range rev {
		out[len(rev)-1-i] = d
	}
	return out
}

func manhattan(ax, ay, bx, by int) int {
	return abs(ax-bx) + abs(ay-by)
}

type astarNode struct {
	f   int
	g   int
	idx int
}

// astarHeap 小顶堆：f 小优先；f 相同优先"更深"（g 大，朝目标方向拉直，避免空旷地形
// 把整个矩形都展开）；再相同按格索引（确定性）。
type astarHeap []astarNode

func (h astarHeap) Len() int { return len(h) }
func (h astarHeap) Less(i, j int) bool {
	if h[i].f != h[j].f {
		return h[i].f < h[j].f
	}
	if h[i].g != h[j].g {
		return h[i].g > h[j].g
	}
	return h[i].idx < h[j].idx
}
func (h astarHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *astarHeap) Push(x any)   { *h = append(*h, x.(astarNode)) }
func (h *astarHeap) Pop() any     { old := *h; n := len(old); v := old[n-1]; *h = old[:n-1]; return v }
