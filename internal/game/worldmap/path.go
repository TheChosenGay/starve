package worldmap

import (
	"container/heap"

	"starve/internal/game/components"
)

// FleeDir 逃跑方向：远离威胁点（avoid），并校验目标格可走（MapData、不出界）。
// 依次尝试"完全反向 + 45° 旋转"的候选，取第一个可走的；都不可走返回 (0,0)（原地）。
func FleeDir(g *MapData, x, y, avoidX, avoidY int) (int, int) {
	dx, dy := 0, 0
	if x != avoidX || y != avoidY {
		dx, dy = signStep(x-avoidX), signStep(y-avoidY)
	}
	candidates := [][2]int{
		{dx, dy}, {dy, -dx}, {-dy, dx}, {dx, -dy},
		{-dx, dy}, {-dx, -dy}, {dy, dx}, {-dy, -dx},
	}
	for _, c := range candidates {
		if c[0] == 0 && c[1] == 0 {
			continue
		}
		nx, ny := x+c[0], y+c[1]
		if g == nil {
			return c[0], c[1]
		}
		if g.Walkable(nx, ny) {
			return c[0], c[1]
		}
	}
	return 0, 0
}

// FindPath A* 网格寻路（曼哈顿启发式 + goal-biased tie-break + 稀疏 visited map）：
// 空旷地形下探索量 ~O(距离)、分配 ~O(路径长度)，长路径/大图显著优于 BFS；
// 确定性：堆按 (f, g 降序, 格索引) 平局（f 相同优先更深，朝目标方向拉直）。
func FindPath(g *MapData, fromX, fromY, toX, toY int) []components.MoveDir {
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
	if _, ok := cameFrom[goal]; !ok {
		return nil
	}
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

func signStep(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
