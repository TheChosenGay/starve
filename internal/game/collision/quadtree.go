package collision

import (
	"math"
	"slices"

	"starve/internal/ecs"
)

// QuadTree 是动态体专用的四叉树（方案 C）。
//
// 与 OrcaAOI 的区别在**自适应**：网格的格子大小固定，实体一密集就退化
// （一个格子里堆很多实体，查询时全要遍历）；四叉树在某个节点的实体数超过
// 阈值时把它**四分**，于是密集处自动细分、稀疏处保持大格子。
//
// 与 BVH 的区别在**切分方式**：BVH 按实体分布二分（切在实体中间，节点可以重叠），
// 四叉树按空间固定四分（严格分区，一个实体只属于一个叶）。
// 四叉树的重建更便宜（不需要自底向上算包围盒），但密集时层级更深。
//
// 本实现是**每 tick 重建**（清空后全量插入）：动态体每帧都在动，
// 增量更新要维护父节点计数、还要处理"实体跨越象限"，重建通常更简单也更快。
// 实体数 2000 量级时，重建是 O(N log N)，实测比查询本身还便宜。
type QuadTree struct {
	// 世界范围。
	minX, minY, maxX, maxY float64
	// nodeCapacity：一个节点超过这么多个实体就四分。太小 -> 层级深；太大 -> 查询时遍历多。
	nodeCapacity int
	// maxDepth：最深层级（防止极小间距时无限细分）。
	maxDepth int

	nodes []quadNode
	// pos 缓存实体位置（查询/判定都用，避免重复读 ECS）。
	pos map[ecs.Entity][2]float64
	// stackBuf 复用遍历栈（避免每次查询分配）。
	stackBuf []int32
	// nodeOf 记录每个实体当前所在的**叶节点**下标（增量更新用）。
	nodeOf map[ecs.Entity]int32
}

type quadNode struct {
	minX, minY, maxX, maxY float64
	// children：四个子节点下标（-1 = 叶）。顺序 NW, NE, SW, SE。
	children [4]int32
	// items：叶节点持有的实体。
	items []ecs.Entity
	// depth：层级（用于 maxDepth 限制）。
	depth int
}

// NewQuadTree 建一棵覆盖 (0,0)-(w,h) 的四叉树。
func NewQuadTree(w, h int) *QuadTree {
	return &QuadTree{
		minX: 0, minY: 0, maxX: float64(w), maxY: float64(h),
		nodeCapacity: 8,
		maxDepth:     12,
		pos:          make(map[ecs.Entity][2]float64),
		nodeOf:       make(map[ecs.Entity]int32),
	}
}

// Reset 清空并重建根节点（复用 nodes 底层数组）。
func (q *QuadTree) Reset() {
	q.nodes = q.nodes[:0]
	q.nodes = append(q.nodes, quadNode{
		minX: q.minX, minY: q.minY, maxX: q.maxX, maxY: q.maxY,
		children: [4]int32{-1, -1, -1, -1}, depth: 0,
	})
}

// Add 插入/更新一个动态体（O(depth)，但**跨象限才真的动**）。
//
// 增量更新：实体所在叶节点的**象限边界**没变时，只更新缓存位置（O(1)）。
// 判断依据是"按当前节点的划分，它还落在同一个象限里吗"——
// 注意这比"格子没变"更宽松：同一个叶节点覆盖一片区域，区域内移动不需要重插。
func (q *QuadTree) Add(e ecs.Entity, x, z float64) {
	q.pos[e] = [2]float64{x, z}
	if leaf, ok := q.nodeOf[e]; ok {
		if q.stillInLeaf(leaf, x, z) {
			return // 还在同一个叶节点覆盖范围内：不用动树
		}
		q.removeFromLeaf(leaf, e)
	}
	q.insert(0, e, x, z)
}

// stillInLeaf 判断 (x,z) 是否仍落在该叶节点覆盖的矩形内。
func (q *QuadTree) stillInLeaf(leaf int32, x, z float64) bool {
	if int(leaf) >= len(q.nodes) {
		return false
	}
	n := &q.nodes[leaf]
	return x >= n.minX && x < n.maxX && z >= n.minY && z < n.maxY
}

// removeFromLeaf 把实体从叶节点摘掉（swap-remove）。
func (q *QuadTree) removeFromLeaf(leaf int32, e ecs.Entity) {
	if int(leaf) >= len(q.nodes) {
		return
	}
	items := q.nodes[leaf].items
	for i, x := range items {
		if x == e {
			last := len(items) - 1
			items[i] = items[last]
			q.nodes[leaf].items = items[:last]
			return
		}
	}
}

// Remove 把实体从树里摘掉（实体死亡/移除时调用）。
func (q *QuadTree) Remove(e ecs.Entity) {
	if leaf, ok := q.nodeOf[e]; ok {
		q.removeFromLeaf(leaf, e)
		delete(q.nodeOf, e)
	}
	delete(q.pos, e)
}

func (q *QuadTree) insert(idx int32, e ecs.Entity, x, z float64) {
	for {
		// 注意：**不要跨 subdivide 持有 &q.nodes[idx]**——subdivide 会 append 到 q.nodes，
		// 切片扩容后旧指针指向已废弃的底层数组，后续写入会丢失（表现为实体凭空消失/
		// 同一实体被重复插入）。所以每次都重新取，且读完字段就立刻用。
		isLeaf := q.nodes[idx].children[0] < 0
		if isLeaf {
			q.nodes[idx].items = append(q.nodes[idx].items, e)
			q.nodeOf[e] = idx // 记录所在叶子（增量更新/移除用）
			if len(q.nodes[idx].items) <= q.nodeCapacity || q.nodes[idx].depth >= q.maxDepth {
				return
			}
			// 拆完之后**不能再 continue 走同一条路径**：subdivide 已经把本节点现有的
			// items（含刚加进去的 e）分发到子节点了，再往下插一次就是重复插入
			// （实测表现为同一实体在多个叶子里各存一份，查询时被重复计数）。
			q.subdivide(idx)
			// subdivide 会把本节点所有实体重新分发，同步它们的 nodeOf。
			return
		}
		idx = q.nodes[idx].children[q.quadrantOf(&q.nodes[idx], x, z)]
	}
}

// quadrantOf 返回 (x,z) 落在节点的哪个象限（0=NW 1=NE 2=SW 3=SE）。
func (q *QuadTree) quadrantOf(n *quadNode, x, z float64) int {
	midX := (n.minX + n.maxX) / 2
	midY := (n.minY + n.maxY) / 2
	c := 0
	if x >= midX {
		c |= 1
	}
	if z >= midY {
		c |= 2
	}
	return c
}

// subdivide 把一个叶节点拆成四个子节点，并重新分配它的实体。
//
// 实现要点（踩过的坑）：**先 append 子节点、再回写父节点的 children**。
// 反过来写（先 n.children = ... 再 append）时，若 append 触发底层数组扩容，
// 父节点那次写入落在**已废弃的旧数组**上，于是父节点看起来仍是叶子
// （children[0] == -1），但它持有的实体已经被分发到子节点 —— 结果同一实体
// 在父叶和子叶里各存一份，查询时被重复返回（实测邻居数虚高 13%）。
func (q *QuadTree) subdivide(idx int32) {
	n := &q.nodes[idx]
	minX, minY, maxX, maxY := n.minX, n.minY, n.maxX, n.maxY
	depth := n.depth + 1
	items := n.items
	midX := (minX + maxX) / 2
	midY := (minY + maxY) / 2

	base := int32(len(q.nodes))
	// 先扩容：这一步可能重新分配底层数组。
	q.nodes = append(q.nodes,
		quadNode{minX: minX, minY: minY, maxX: midX, maxY: midY,
			children: [4]int32{-1, -1, -1, -1}, depth: depth},
		quadNode{minX: midX, minY: minY, maxX: maxX, maxY: midY,
			children: [4]int32{-1, -1, -1, -1}, depth: depth},
		quadNode{minX: minX, minY: midY, maxX: midX, maxY: maxY,
			children: [4]int32{-1, -1, -1, -1}, depth: depth},
		quadNode{minX: midX, minY: midY, maxX: maxX, maxY: maxY,
			children: [4]int32{-1, -1, -1, -1}, depth: depth},
	)
	// append 之后再回写父节点（此刻 q.nodes 已是最终数组）。
	p := &q.nodes[idx]
	p.children = [4]int32{base, base + 1, base + 2, base + 3}
	p.items = nil
	// 分发旧实体到对应象限（insert 会为每个实体写入新的 nodeOf）。
	children := p.children
	for _, e := range items {
		pos := q.pos[e]
		q.insert(children[q.quadrantOf(&q.nodes[idx], pos[0], pos[1])], e, pos[0], pos[1])
	}
}

// Range 遍历与 (x,z) 半径 r 的圆相交的叶节点里的实体（宽阶段）。
func (q *QuadTree) Range(x, z, r float64, fn func(e ecs.Entity) bool) bool {
	if len(q.nodes) == 0 {
		return true
	}
	stack := q.stackBuf[:0]
	stack = append(stack, 0)
	for len(stack) > 0 {
		idx := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &q.nodes[idx]
		if !circleIntersectsBox(x, z, r, n.minX, n.minY, n.maxX, n.maxY) {
			continue
		}
		if n.children[0] < 0 {
			for _, e := range n.items {
				if !fn(e) {
					q.stackBuf = stack[:0]
					return false
				}
			}
			continue
		}
		stack = append(stack, n.children[0], n.children[1], n.children[2], n.children[3])
	}
	q.stackBuf = stack[:0]
	return true
}

// circleIntersectsBox 判断圆与 AABB 是否相交（最近点距离法）。
func circleIntersectsBox(x, z, r, minX, minY, maxX, maxY float64) bool {
	qx := math.Max(minX, math.Min(x, maxX))
	qy := math.Max(minY, math.Min(z, maxY))
	dx := x - qx
	dy := z - qy
	return dx*dx+dy*dy <= r*r
}

// Pos 返回缓存的实体位置。
func (q *QuadTree) Pos(e ecs.Entity) (float64, float64, bool) {
	p, ok := q.pos[e]
	return p[0], p[1], ok
}

// Len 返回树里的实体总数。
func (q *QuadTree) Len() int { return len(q.pos) }

// NodeCount 返回节点数（观测用：看树是否退化）。
func (q *QuadTree) NodeCount() int { return len(q.nodes) }

// Neighbors 用四叉树收集邻居：与 OrcaAOI.Neighbors 同语义
// （粗筛 + 精确距离 + 按 id 排序），保证两方案与 BVH 基线给出同一集合。
func (q *QuadTree) Neighbors(
	x, z, r float64, exclude ecs.Entity,
	shapeOf ShapeOf,
	buf []Neighbor,
) []Neighbor {
	out := buf[:0]
	maxR := r + maxNeighborRadius
	q.Range(x, z, maxR, func(e ecs.Entity) bool {
		if e == exclude {
			return true
		}
		ex, ez, ok := q.Pos(e)
		if !ok {
			return true
		}
		nr, half := shapeOf(e)
		reach := r + nr + half
		dx := ex - x
		dz := ez - z
		if dx*dx+dz*dz > reach*reach {
			return true
		}
		out = append(out, Neighbor{Entity: e, X: ex, Z: ez, Radius: nr, HalfLength: half})
		return true
	})
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
