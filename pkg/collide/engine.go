package collide

import "math"

// Result 是一次命中的结果：谁被命中、命中在哪个部位、法向与深度/距离。
//
// Point 是图元表面上的点（碰撞部位），可以直接用来挂命中特效、贴花、溅血位置。
// 各类查询的 Normal 语义：
//   - Overlap：接触法向，从被查询方指向查询方（与 Contact 一致）；
//   - OverlapSector：从查询中心指向命中点（也就是击退方向）；
//   - Raycast / SweepHit：图元表面法向，指向射线/扫掠体的来向。
type Result struct {
	Handle Handle
	Point  Vec3
	Normal Vec3
	Dist   float64 // 射线 / 扫掠的距离；重叠类查询为 0
	T      float64 // 扫掠比例 [0,1]；其余为 0
	Depth  float64 // 穿透深度；射线 / 扫掠为 0
}

// Filter 决定某个代理是否参与本次查询：返回 true 表示参与。
// nil 表示全部参与。典型用法是排除攻击者自己或非敌对阵营。
type Filter func(h Handle) bool

// EngineOptions 是引擎配置。
type EngineOptions struct {
	// Margin 是 fat AABB 的外扩量。它决定了"小幅移动不用动索引"的容差，
	// 由调用方按玩法给（静态物 0；高速物体按单帧最大位移）。
	Margin float64
	// Scanner 为 nil 时使用默认的 ArrayScanner。
	Scanner Scanner
}

// Engine 把"图元 → 宽阶段代理"的生命周期与空间查询绑在一起。
// 调用方 Add 一个图元拿到句柄，之后所有查询都通过引擎进行。
type Engine struct {
	scanner Scanner
	margin  float64

	shapes []Solid
	boxes  []AABB // fat AABB：宽阶段的真值
	gens   []uint32
	live   []bool
	free   []uint32
	count  int
}

// New 创建一个空引擎。
func NewEngine(opts EngineOptions) *Engine {
	sc := opts.Scanner
	if sc == nil {
		sc = NewArrayScanner()
	}
	return &Engine{scanner: sc, margin: opts.Margin}
}

// Reserve 预分配槽位容量，避免批量 Add 时反复扩容。
func (e *Engine) Reserve(n int) {
	if n <= len(e.shapes) {
		return
	}
	shapes := make([]Solid, len(e.shapes), n)
	copy(shapes, e.shapes)
	e.shapes = shapes
	boxes := make([]AABB, len(e.boxes), n)
	copy(boxes, e.boxes)
	e.boxes = boxes
	gens := make([]uint32, len(e.gens), n)
	copy(gens, e.gens)
	e.gens = gens
	live := make([]bool, len(e.live), n)
	copy(live, e.live)
	e.live = live
}

// Len 返回代理数量。
func (e *Engine) Len() int { return e.count }

// Scanner 返回底层扫描器（只读用途，例如查询候选数统计）。
func (e *Engine) Scanner() Scanner { return e.scanner }

// Add 注册一个图元并返回句柄。句柄在 Remove 之前一直有效。
func (e *Engine) Add(s Solid) Handle {
	var slot uint32
	if n := len(e.free); n > 0 {
		slot = e.free[n-1]
		e.free = e.free[:n-1]
	} else {
		slot = uint32(len(e.shapes))
		e.shapes = append(e.shapes, nil)
		e.boxes = append(e.boxes, AABB{})
		e.gens = append(e.gens, 0)
		e.live = append(e.live, false)
	}
	e.shapes[slot] = s
	e.boxes[slot] = FatAABB(s.Bounds(), e.margin)
	e.live[slot] = true
	e.count++
	h := makeHandle(slot, e.gens[slot])
	e.scanner.Insert(h, e.boxes[slot])
	return h
}

// Update 用新的图元整体替换句柄名下的图元。
//
// 语义要点：
//   - 句柄身份不变（不像 Remove + Add 那样换身份）；
//   - 图元只读：加入后不要再改它指向的字，所有修改都走这里；
//   - 幂等：新图元仍在旧 fat AABB 内时，索引一次都不会被碰。
func (e *Engine) Update(h Handle, s Solid) {
	i := e.slot(h)
	e.shapes[i] = s
	tight := s.Bounds()
	if e.boxes[i].ContainsBox(tight) {
		return
	}
	e.boxes[i] = FatAABB(tight, e.margin)
	e.scanner.Update(h, e.boxes[i])
}

// Remove 注销一个代理：句柄随即失效（再用会 panic，而不是指向别的对象）。
func (e *Engine) Remove(h Handle) {
	i := e.slot(h)
	e.scanner.Remove(h)
	e.shapes[i] = nil
	e.live[i] = false
	e.gens[i]++
	e.count--
	e.free = append(e.free, i)
}

// Has 判断句柄当前是否有效。
func (e *Engine) Has(h Handle) bool {
	i := h.Slot()
	return int(i) < len(e.gens) && e.live[i] && e.gens[i] == h.Gen()
}

// Shape 返回句柄名下的图元。
func (e *Engine) Shape(h Handle) Solid { return e.shapes[e.slot(h)] }

// Box 返回句柄名下代理的 fat AABB（宽阶段的真值）。
func (e *Engine) Box(h Handle) AABB { return e.boxes[e.slot(h)] }

// Rebuild 全量重建索引。
func (e *Engine) Rebuild() {
	e.scanner.Reset()
	for i := range e.live {
		if !e.live[i] {
			continue
		}
		e.scanner.Insert(makeHandle(uint32(i), e.gens[i]), e.boxes[i])
	}
}

// slot 校验句柄并返回槽位；失效句柄直接 panic。
func (e *Engine) slot(h Handle) uint32 {
	i := h.Slot()
	if int(i) >= len(e.gens) || !e.live[i] || e.gens[i] != h.Gen() {
		panic("broad: 无效或已失效的句柄")
	}
	return i
}

// Query 只做宽阶段：把与 box 重叠的候选句柄回调出去，不做窄阶段。
func (e *Engine) Query(box AABB, fn func(h Handle) bool) {
	e.scanner.Query(box, fn)
}

// Overlap 形状重叠查询：只把真正相交的代理回调出去，并附带接触点 / 法向 / 深度。
// 返回是否至少命中一次。
func (e *Engine) Overlap(q Solid, f Filter, fn func(r Result) bool) bool {
	found := false
	e.scanner.Query(q.Bounds(), func(h Handle) bool {
		if f != nil && !f(h) {
			return true
		}
		ct, ok := ContactShapes(q, e.Shape(h))
		if !ok {
			return true
		}
		found = true
		return fn(Result{Handle: h, Point: ct.Point, Normal: ct.Normal, Depth: ct.Depth})
	})
	return found
}

// OverlapSector 扇形查询（挥砍、技能锥）：命中结果里带上目标身上朝向扇心的点，
// 也就是"被砍中的部位"，可以直接拿去画局部变红 / 溅血 / 贴花。
//
// 判定档位：取目标"最朝向扇心的表面点"做点判定（角度 + 距离带）。
// 已知边界：目标只有很小一部分探进扇区、而最近表面点仍在扇区外时不会命中；
// 要彻底消除这种边缘误判，需要在 collide 里按图元求扇形最近点。
func (e *Engine) OverlapSector(s Sector, f Filter, fn func(r Result) bool) bool {
	found := false
	e.scanner.Query(s.Bounds(), func(h Handle) bool {
		if f != nil && !f(h) {
			return true
		}
		target := e.Shape(h)
		p := ClosestSurfacePoint(s.Center, target)
		if !s.Contains(p, 0) {
			return true
		}
		found = true
		return fn(Result{
			Handle: h,
			Point:  p,
			Normal: safeNormal(p.Sub(s.Center)), // 击退方向：远离扇心
			Dist:   math.Hypot(p.X-s.Center.X, p.Z-s.Center.Z),
		})
	})
	return found
}

// Raycast 返回射线上最近的一次命中。dir 需为单位向量。
func (e *Engine) Raycast(o, dir Vec3, maxDist float64, f Filter) (Result, bool) {
	seg := Segment{A: o, B: o.Add(dir.Scale(maxDist))}
	best := Result{}
	bestDist := maxDist
	found := false
	e.scanner.Query(seg.Bounds(), func(h Handle) bool {
		if f != nil && !f(h) {
			return true
		}
		hit := IntersectRayShape(o, dir, bestDist, e.Shape(h))
		if !hit.Hit {
			return true
		}
		bestDist = hit.Dist
		best = Result{Handle: h, Point: hit.Point, Normal: hit.Normal, Dist: hit.Dist}
		found = true
		return true // 必须扫完才能保证"最近"
	})
	return best, found
}

// RaycastAll 把射线上所有命中按距离排序后回调（穿透弹、多重命中用）。
func (e *Engine) RaycastAll(o, dir Vec3, maxDist float64, f Filter) []Result {
	out := make([]Result, 0, 4)
	e.scanner.Query(Segment{A: o, B: o.Add(dir.Scale(maxDist))}.Bounds(), func(h Handle) bool {
		if f != nil && !f(h) {
			return true
		}
		hit := IntersectRayShape(o, dir, maxDist, e.Shape(h))
		if !hit.Hit {
			return true
		}
		out = append(out, Result{Handle: h, Point: hit.Point, Normal: hit.Normal, Dist: hit.Dist})
		return true
	})
	sortResultsByDist(out)
	return out
}

// SweepHit 让移动体 s 沿 motion 平移，返回最早接触到的代理。
// 目前只支持 Sphere 作为移动体（见 SweepShapes）。
func (e *Engine) SweepHit(s Solid, motion Vec3, f Filter) (Result, bool) {
	box := SweptAABB(s.Bounds(), motion)
	best := Result{}
	found := false
	e.scanner.Query(box, func(h Handle) bool {
		if f != nil && !f(h) {
			return true
		}
		hit := SweepShapes(s, motion, e.Shape(h))
		if !hit.Hit {
			return true
		}
		if !found || hit.T < best.T {
			best = Result{Handle: h, Point: hit.Point, Normal: hit.Normal, Dist: hit.Dist, T: hit.T}
			found = true
		}
		return true
	})
	return best, found
}

func sortResultsByDist(rs []Result) {
	for i := 1; i < len(rs); i++ {
		for j := i; j > 0 && rs[j].Dist < rs[j-1].Dist; j-- {
			rs[j], rs[j-1] = rs[j-1], rs[j]
		}
	}
}
