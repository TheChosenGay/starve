package collide

// BVHScanner 是宽阶段的动态 AABB 树（BVH）：每个内部节点存两个孩子的包围盒并集，
// 每个叶子存一个代理的 fat AABB 与句柄。查询从根往下走，只访问与查询盒相交的子树——
// 于是盒子测试从 O(N) 降到 O(log N + 命中数)。
//
// 与 ArrayScanner 的关系：两者实现同一个 Scanner 接口，可以互换。
// 数组扫描器不做空间加速，但已经把"先粗筛再精算"这件事做了；
// BVH 把粗筛本身也降到对数级，物体上万时差距会拉开一个数量级。
//
// 实现要点（也都是坑）：
//   - 节点存在切片里、相互引用用下标而不是指针：池扩容时不会悬空，也没有指针扫描；
//   - 空闲节点复用（free list）：动态树每次插入删除都碰节点，不复用就是 GC 重灾区；
//   - 插入用"合并后表面积增量最小"的贪心下降选兄弟，再向上 refit；
//   - 删除用兄弟顶替父节点并向上 refit；refit 时做一次单旋转，避免树被删歪；
//   - Build 是自顶向下按最长轴中位数切分（O(N log²N)），比 N 次 Insert 出来的树质量好得多。
//
// 树形依赖操作历史，所以候选的**枚举顺序**会随历史漂移；引擎的语义化查询不依赖这个顺序
// （需要"最近 / 最先"时由引擎自己比较 T / Dist），但若要跨实现逐位复现，
// 需要在引擎边界按句柄排序——这一点与 ArrayScanner 的差异是设计上承认的。
type BVHScanner struct {
	nodes []bvhNode
	free  []int32
	index map[Handle]int32
	root  int32
	count int

	// 查询与建树用的复用缓冲（单线程使用，和 Engine 的假设一致）
	stack    []int32
	buildIdx []int32

	// Visits 统计 Query 访问过的节点数（含被剔除的），用于观测与演示，调用方可自行清零。
	// 它与 O(N) 的线性扫描形成对照：BVH 每次查询只碰 O(log N) 个节点。
	Visits uint64
}

const bvhNull int32 = -1

type bvhNode struct {
	box    AABB
	parent int32
	child  [2]int32
	height int32 // 叶子 = 0，内部 = 1 + max(孩子)
	handle Handle
	leaf   bool
}

// NewBVHScanner 返回一个空的动态 AABB 树。
func NewBVHScanner() *BVHScanner {
	return &BVHScanner{index: make(map[Handle]int32), root: bvhNull}
}

// Len 返回代理数量。
func (t *BVHScanner) Len() int { return t.count }

// Height 返回树高（诊断用：树高失控说明插入/删除把树弄退化了）。
func (t *BVHScanner) Height() int {
	if t.root == bvhNull {
		return 0
	}
	return int(t.nodes[t.root].height) + 1
}

// Reset 清空整棵树。
func (t *BVHScanner) Reset() {
	t.nodes = t.nodes[:0]
	t.free = t.free[:0]
	t.root = bvhNull
	t.count = 0
	clear(t.index)
}

// Build 自顶向下重建：按最长轴的中位数把叶子一分为二、递归到底。
// 同一批代理用 Build 出来的树，查询通常明显快于 N 次 Insert 的结果。
func (t *BVHScanner) Build(hs []Handle, boxes []AABB) {
	t.Reset()
	n := len(hs)
	if n == 0 || n != len(boxes) {
		return
	}
	t.nodes = make([]bvhNode, 0, 2*n-1)
	t.buildIdx = t.buildIdx[:0]
	for i := 0; i < n; i++ {
		t.nodes = append(t.nodes, bvhNode{
			box: boxes[i], parent: bvhNull, child: [2]int32{bvhNull, bvhNull},
			handle: hs[i], leaf: true,
		})
		t.index[hs[i]] = int32(i)
		t.buildIdx = append(t.buildIdx, int32(i))
	}
	t.count = n
	t.root = t.buildRange(0, n)
}

func (t *BVHScanner) buildRange(lo, hi int) int32 {
	n := hi - lo
	if n == 1 {
		return t.buildIdx[lo]
	}
	// 取质心包围盒的最长轴做切分轴（比按盒子本身更长轴的分布更均匀）
	cmin := t.centroid(t.buildIdx[lo])
	cmax := cmin
	for i := lo + 1; i < hi; i++ {
		c := t.centroid(t.buildIdx[i])
		cmin, cmax = cmin.Min(c), cmax.Max(c)
	}
	ext := cmax.Sub(cmin)
	axis := 0
	if ext.Y > ext.At(axis) {
		axis = 1
	}
	if ext.Z > ext.At(axis) {
		axis = 2
	}
	// 只做"选出中位数"而不是整段排序：O(N) 划分代替 O(N log N) 排序，
	// 且不产生 sort.Slice 的闭包分配（重建会被反复调用）。
	mid := lo + n/2
	t.nthElement(t.buildIdx[lo:hi], axis, mid-lo)
	left, right := t.buildRange(lo, mid), t.buildRange(mid, hi)
	p := t.newNode()
	t.nodes[left].parent = p
	t.nodes[right].parent = p
	t.nodes[p].leaf = false
	t.nodes[p].child = [2]int32{left, right}
	t.refit(p)
	return p
}

func (t *BVHScanner) centroid(i int32) Vec3 { return t.nodes[i].box.Center() }

// nthElement 把 seg 原地划分成「< 第 k 个、== 第 k 个、> 第 k 个」三段（按质心在选择轴上的值）。
// 用三点取中的枢轴，避免空间上已经有序的数据把快选拖成 O(N²)。
func (t *BVHScanner) nthElement(seg []int32, axis, k int) {
	less := func(a, b int32) bool {
		ca, cb := t.centroid(a).At(axis), t.centroid(b).At(axis)
		if ca != cb {
			return ca < cb
		}
		return a < b // 平局按节点号，保证建树结果可复现
	}
	lo, hi := 0, len(seg)-1
	for lo < hi {
		mid := lo + (hi-lo)/2
		// 三点取中
		if less(seg[mid], seg[lo]) {
			seg[mid], seg[lo] = seg[lo], seg[mid]
		}
		if less(seg[hi], seg[lo]) {
			seg[hi], seg[lo] = seg[lo], seg[hi]
		}
		if less(seg[hi], seg[mid]) {
			seg[hi], seg[mid] = seg[mid], seg[hi]
		}
		pivot := seg[mid]
		i, j := lo, hi
		for i <= j {
			for less(seg[i], pivot) {
				i++
			}
			for less(pivot, seg[j]) {
				j--
			}
			if i <= j {
				seg[i], seg[j] = seg[j], seg[i]
				i++
				j--
			}
		}
		switch {
		case k <= j:
			hi = j
		case k >= i:
			lo = i
		default:
			return
		}
	}
}

// Insert 插入一个代理。
func (t *BVHScanner) Insert(h Handle, b AABB) {
	if _, ok := t.index[h]; ok {
		t.Update(h, b)
		return
	}
	leaf := t.newNode()
	t.nodes[leaf].leaf = true
	t.nodes[leaf].box = b
	t.nodes[leaf].handle = h
	t.nodes[leaf].height = 0
	t.index[h] = leaf
	t.count++
	t.insertLeaf(leaf)
}

// Update 改一个代理的包围盒：还在旧盒里就什么都不做，否则摘下来重插。
func (t *BVHScanner) Update(h Handle, b AABB) {
	leaf, ok := t.index[h]
	if !ok {
		t.Insert(h, b)
		return
	}
	if t.nodes[leaf].box.ContainsBox(b) {
		return // 还在 fat AABB 内：树一次都不碰
	}
	// 摘下来重插，但**不能回收节点**——回收会把 leaf / handle 一起清掉，
	// 再插回去就成了"没有孩子的内部节点"，树立刻损坏（查询会漏、插入会转圈）。
	t.detach(leaf)
	t.nodes[leaf].box = b
	t.nodes[leaf].parent = bvhNull
	t.insertLeaf(leaf)
}

// Remove 摘掉一个代理。
func (t *BVHScanner) Remove(h Handle) {
	leaf, ok := t.index[h]
	if !ok {
		return
	}
	delete(t.index, h)
	t.count--
	t.removeLeaf(leaf)
}

// Query 遍历与 box 相交的子树，把叶子句柄回调出去；fn 返回 false 立即停止。
func (t *BVHScanner) Query(box AABB, fn func(h Handle) bool) {
	if t.root == bvhNull {
		return
	}
	st := t.stack[:0]
	st = append(st, t.root)
	for len(st) > 0 {
		i := st[len(st)-1]
		st = st[:len(st)-1]
		node := t.nodes[i]
		t.Visits++
		if !TestAABBAABB(box, node.box) {
			continue
		}
		if node.leaf {
			if !fn(node.handle) {
				break
			}
			continue
		}
		st = append(st, node.child[0], node.child[1])
	}
	t.stack = st[:0]
}

// ---- 内部 ----

func (t *BVHScanner) newNode() int32 {
	if n := len(t.free); n > 0 {
		i := t.free[n-1]
		t.free = t.free[:n-1]
		t.nodes[i] = bvhNode{parent: bvhNull, child: [2]int32{bvhNull, bvhNull}}
		return i
	}
	t.nodes = append(t.nodes, bvhNode{parent: bvhNull, child: [2]int32{bvhNull, bvhNull}})
	return int32(len(t.nodes) - 1)
}

func (t *BVHScanner) freeNode(i int32) {
	t.nodes[i] = bvhNode{parent: bvhNull, child: [2]int32{bvhNull, bvhNull}}
	t.free = append(t.free, i)
}

// refit 重算节点的高度与包围盒（必须先保证孩子已经是最新的）。
func (t *BVHScanner) refit(i int32) {
	c0, c1 := t.nodes[i].child[0], t.nodes[i].child[1]
	t.nodes[i].height = 1 + maxInt32(t.nodes[c0].height, t.nodes[c1].height)
	t.nodes[i].box = t.nodes[c0].box.Union(t.nodes[c1].box)
}

// surfaceArea 是包围盒表面积（用半长算，差一个常数因子，只用于比较大小）。
func surfaceArea(b AABB) float64 {
	e := b.Extents()
	return 4 * (e.X*e.Y + e.Y*e.Z + e.Z*e.X)
}

func (t *BVHScanner) insertLeaf(leaf int32) {
	if t.root == bvhNull {
		t.root = leaf
		t.nodes[leaf].parent = bvhNull
		return
	}
	box := t.nodes[leaf].box
	// 贪心下降：每一步选"与新区块合并后表面积增量更小"的那一侧
	idx := t.root
	for !t.nodes[idx].leaf {
		c0, c1 := t.nodes[idx].child[0], t.nodes[idx].child[1]
		cost0 := surfaceArea(t.nodes[c0].box.Union(box)) - surfaceArea(t.nodes[c0].box)
		cost1 := surfaceArea(t.nodes[c1].box.Union(box)) - surfaceArea(t.nodes[c1].box)
		if cost0 <= cost1 {
			idx = c0
		} else {
			idx = c1
		}
	}
	sibling := idx
	oldParent := t.nodes[sibling].parent
	parent := t.newNode()
	t.nodes[parent].leaf = false
	t.nodes[parent].parent = oldParent
	t.nodes[parent].child = [2]int32{sibling, leaf}
	t.nodes[parent].box = t.nodes[sibling].box.Union(box)
	t.nodes[parent].height = t.nodes[sibling].height + 1
	t.nodes[sibling].parent = parent
	t.nodes[leaf].parent = parent
	if oldParent == bvhNull {
		t.root = parent
		return
	}
	if t.nodes[oldParent].child[0] == sibling {
		t.nodes[oldParent].child[0] = parent
	} else {
		t.nodes[oldParent].child[1] = parent
	}
	for j := oldParent; j != bvhNull; j = t.nodes[j].parent {
		j = t.balance(j)
		t.refit(j)
	}
}

func (t *BVHScanner) removeLeaf(leaf int32) {
	t.detach(leaf)
	t.freeNode(leaf)
}

// detach 把叶子从树上摘下来（不回收节点，Update 会重新插入同一个节点）。
func (t *BVHScanner) detach(leaf int32) {
	if leaf == t.root {
		t.root = bvhNull
		return
	}
	parent := t.nodes[leaf].parent
	grand := t.nodes[parent].parent
	sibling := t.nodes[parent].child[0]
	if sibling == leaf {
		sibling = t.nodes[parent].child[1]
	}
	t.nodes[sibling].parent = grand
	if grand == bvhNull {
		t.root = sibling
	} else {
		if t.nodes[grand].child[0] == parent {
			t.nodes[grand].child[0] = sibling
		} else {
			t.nodes[grand].child[1] = sibling
		}
		for j := grand; j != bvhNull; j = t.nodes[j].parent {
			j = t.balance(j)
			t.refit(j)
		}
	}
	t.freeNode(parent)
}

// balance 在 refit 路径上做一次单旋转：两侧子树高度差超过 1 就把高的那侧提上来。
// 没有它，反复插入删除会把树拉成一条链，查询退化回线性。
func (t *BVHScanner) balance(i int32) int32 {
	if t.nodes[i].leaf || t.nodes[i].height < 2 {
		return i
	}
	c0, c1 := t.nodes[i].child[0], t.nodes[i].child[1]
	switch {
	case t.nodes[c1].height > t.nodes[c0].height+1:
		// 把 c1 提上来：c1 的左孩子接到 i 的右孩子位置
		f := t.nodes[c1].child[0]
		t.nodes[i].child[1] = f
		t.nodes[f].parent = i
		t.nodes[c1].child[0] = i
		t.nodes[c1].parent = t.nodes[i].parent
		t.nodes[i].parent = c1
		t.reparent(i, c1)
		t.refit(i)
		t.refit(c1)
		return c1
	case t.nodes[c0].height > t.nodes[c1].height+1:
		// 把 c0 提上来：c0 的右孩子接到 i 的左孩子位置
		e := t.nodes[c0].child[1]
		t.nodes[i].child[0] = e
		t.nodes[e].parent = i
		t.nodes[c0].child[1] = i
		t.nodes[c0].parent = t.nodes[i].parent
		t.nodes[i].parent = c0
		t.reparent(i, c0)
		t.refit(i)
		t.refit(c0)
		return c0
	}
	return i
}

// reparent 把 i 的位置（父节点的孩子指针或根）换成 n。
func (t *BVHScanner) reparent(i, n int32) {
	p := t.nodes[n].parent
	if p == bvhNull {
		t.root = n
		return
	}
	if t.nodes[p].child[0] == i {
		t.nodes[p].child[0] = n
	} else {
		t.nodes[p].child[1] = n
	}
}

func maxInt32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
