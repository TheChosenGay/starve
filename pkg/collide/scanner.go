package collide

// Handle 是引擎发给每个代理的句柄：低 32 位是槽位下标，高 32 位是代次。
//
// 代次的意义：槽位会被复用（对象销毁后新对象占同一个下标），
// 带代次才能让旧句柄自动失效，而不是悄悄指向新对象（ABA）。
type Handle uint64

// Slot 返回槽位下标。
func (h Handle) Slot() uint32 { return uint32(h) }

// Gen 返回代次。
func (h Handle) Gen() uint32 { return uint32(h >> 32) }

func makeHandle(slot, gen uint32) Handle { return Handle(uint64(gen)<<32 | uint64(slot)) }

// Querier 是扫描器的只读能力：把与 box 重叠的代理句柄回调出去。
//
// box 允许带 ±Inf（例如扇形在竖直方向不设限）。
// 同一个句柄在一次 Query 里只会被回调一次；回调顺序在给定实现下是确定的。
type Querier interface {
	Query(box AABB, fn func(h Handle) bool)
}

// Scanner 是宽阶段的空间扫描器：维护一批 AABB 代理，回答"哪些代理与给定盒重叠"。
//
// 名字里的"扫描"指遍历索引结构（数组 / BVH 子树 / 网格单元），不是遍历全部代理——
// 它存在的意义恰恰是少扫。
type Scanner interface {
	Querier
	Reset()
	// Build 用一次全量重建替代逐个 Insert：数组实现只是重填切片，
	// 树实现可以自顶向下切分（比 N 次 Insert 出来的树质量好得多）。
	Build(hs []Handle, boxes []AABB)
	Insert(h Handle, b AABB)
	Update(h Handle, b AABB)
	Remove(h Handle)
	Len() int
}

// ArrayScanner 是默认扫描器：一个普通数组 + 线性扫描。
//
// 它不做空间加速，但已经把宽阶段最值钱的那一半做了：先用极便宜的 AABB 判定剔除，
// 只有候选才进窄阶段（实测 AABB 剔除比窄阶段便宜 20–100 倍）。
// 查询代价是 O(N)，代理上万时它会成为瓶颈——那时换成 BVH 扫描器即可，
// Scanner 接口不变，上层与测试都不用改。
type ArrayScanner struct {
	handles []Handle
	boxes   []AABB
	index   map[Handle]int
}

// NewArrayScanner 返回一个空数组扫描器。
func NewArrayScanner() *ArrayScanner {
	return &ArrayScanner{index: make(map[Handle]int)}
}

// Reset 清空全部代理。
func (s *ArrayScanner) Reset() {
	s.handles = s.handles[:0]
	s.boxes = s.boxes[:0]
	clear(s.index)
}

// Build 全量重建（数组实现就是重填一遍）。
func (s *ArrayScanner) Build(hs []Handle, boxes []AABB) {
	s.Reset()
	n := len(hs)
	if n != len(boxes) {
		return
	}
	s.handles = append(s.handles[:0], hs...)
	s.boxes = append(s.boxes[:0], boxes...)
	for i, h := range hs {
		s.index[h] = i
	}
}

// Insert 加入一个代理。
func (s *ArrayScanner) Insert(h Handle, b AABB) {
	s.index[h] = len(s.handles)
	s.handles = append(s.handles, h)
	s.boxes = append(s.boxes, b)
}

// Update 就地改一个代理的盒（数组扫描器里没有结构要维护，直接改）。
func (s *ArrayScanner) Update(h Handle, b AABB) {
	if i, ok := s.index[h]; ok {
		s.boxes[i] = b
	}
}

// Remove 删掉一个代理（交换删除，保持数组稠密）。
func (s *ArrayScanner) Remove(h Handle) {
	i, ok := s.index[h]
	if !ok {
		return
	}
	last := len(s.handles) - 1
	if i != last {
		s.handles[i] = s.handles[last]
		s.boxes[i] = s.boxes[last]
		s.index[s.handles[i]] = i
	}
	s.handles = s.handles[:last]
	s.boxes = s.boxes[:last]
	delete(s.index, h)
}

// Len 返回代理数量。
func (s *ArrayScanner) Len() int { return len(s.handles) }

// Query 线性扫描，与 box 重叠的代理逐个回调；fn 返回 false 立即停止。
func (s *ArrayScanner) Query(box AABB, fn func(h Handle) bool) {
	for i := range s.handles {
		if !TestAABBAABB(box, s.boxes[i]) {
			continue
		}
		if !fn(s.handles[i]) {
			return
		}
	}
}
