package collide

import (
	"math"
	"math/rand"
	"testing"
)

// countingScanner 包一层 ArrayScanner，记录索引操作次数——用来证明
// "还在 fat AABB 里就不动索引"这条语义真的生效。
type countingScanner struct {
	*ArrayScanner
	updates int
	inserts int
}

func newCountingScanner() *countingScanner {
	return &countingScanner{ArrayScanner: NewArrayScanner()}
}

func (s *countingScanner) Insert(h Handle, b AABB) {
	s.inserts++
	s.ArrayScanner.Insert(h, b)
}

func (s *countingScanner) Update(h Handle, b AABB) {
	s.updates++
	s.ArrayScanner.Update(h, b)
}

func TestFatAABBUpdateSemantics(t *testing.T) {
	sc := newCountingScanner()
	e := NewEngine(EngineOptions{Margin: 0.5, Scanner: sc})

	body := Capsule{A: Vec3{Y: 0}, B: Vec3{Y: 1.8}, R: 0.35}
	h := e.Add(body)
	if sc.inserts != 1 {
		t.Fatalf("Add 应当插一次索引，实际 %d", sc.inserts)
	}

	// 小幅移动：仍在旧 fat AABB 内 → 索引一次都不该被碰。
	moved := body
	moved.A.X += 0.2
	moved.B.X += 0.2
	e.Update(h, moved)
	if sc.updates != 0 {
		t.Fatalf("fat AABB 内的移动不应动索引，实际 Update 了 %d 次", sc.updates)
	}
	if e.Shape(h) != Solid(moved) {
		t.Fatal("图元记录仍应被更新（只有索引不动）")
	}

	// 大幅移动：逃出 fat AABB → 索引应当被更新一次。
	far := body
	far.A.X += 5
	far.B.X += 5
	e.Update(h, far)
	if sc.updates != 1 {
		t.Fatalf("逃出 fat AABB 后应更新一次索引，实际 %d", sc.updates)
	}

	// 幂等：同一个图元再 Update 一次，不该再动索引。
	e.Update(h, far)
	if sc.updates != 1 {
		t.Fatalf("重复 Update 同一图元不应再动索引，实际 %d", sc.updates)
	}

	// margin=0 时任何移动都会逃出，这是调用方的选择。
	sc2 := newCountingScanner()
	e2 := NewEngine(EngineOptions{Margin: 0, Scanner: sc2})
	h2 := e2.Add(body)
	e2.Update(h2, moved)
	if sc2.updates != 1 {
		t.Fatalf("margin=0 时移动应当更新索引，实际 %d", sc2.updates)
	}
}

func TestHandleLifecycleAndGenerations(t *testing.T) {
	e := NewEngine(EngineOptions{})
	a := e.Add(Sphere{C: Vec3{X: 0}, R: 0.5})
	b := e.Add(Sphere{C: Vec3{X: 3}, R: 0.5})
	if !e.Has(a) || !e.Has(b) || e.Len() != 2 {
		t.Fatalf("两个句柄都应有效，Len=%d", e.Len())
	}
	e.Remove(a)
	if e.Has(a) {
		t.Fatal("Remove 后句柄应失效")
	}
	if e.Len() != 1 {
		t.Fatalf("Remove 后 Len 应为 1，实际 %d", e.Len())
	}

	// 槽位复用：新对象拿到同一个槽位，但代次不同。
	c := e.Add(Sphere{C: Vec3{X: 6}, R: 0.5})
	if c.Slot() != a.Slot() {
		t.Fatalf("应当复用槽位 %d，实际 %d", a.Slot(), c.Slot())
	}
	if c.Gen() == a.Gen() {
		t.Fatal("复用的槽位必须换新代次")
	}
	for _, stale := range []Handle{a} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("使用失效句柄应当 panic")
				}
			}()
			e.Shape(stale)
		}()
	}
	// b 不受影响。
	if !e.Has(b) {
		t.Fatal("其它句柄不应受影响")
	}
	if !e.Has(c) {
		t.Fatal("新句柄应当有效")
	}
}

// TestOverlapMatchesBruteForce：引擎查到的命中集合必须与"遍历所有图元"完全一致。
func TestOverlapMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const n = 300
	e := NewEngine(EngineOptions{Margin: 0.1})
	e.Reserve(n)

	shapes := make([]Solid, 0, n)
	handles := make([]Handle, 0, n)
	for i := 0; i < n; i++ {
		c := Capsule{
			A: Vec3{X: rng.Float64()*40 - 20, Z: rng.Float64()*40 - 20},
			R: 0.3 + rng.Float64()*0.3,
		}
		c.B = c.A.Add(Vec3{Y: 1.8})
		shapes = append(shapes, c)
		handles = append(handles, e.Add(c))
	}

	for q := 0; q < 50; q++ {
		query := Sphere{
			C: Vec3{X: rng.Float64()*40 - 20, Y: 1, Z: rng.Float64()*40 - 20},
			R: 1 + rng.Float64()*2,
		}
		want := map[Handle]bool{}
		for i, s := range shapes {
			if TestShapes(query, s) {
				want[handles[i]] = true
			}
		}
		got := map[Handle]bool{}
		e.Overlap(query, nil, func(r Result) bool {
			got[r.Handle] = true
			return true
		})
		if len(got) != len(want) {
			t.Fatalf("查询 %d：引擎 %d 个命中，暴力 %d 个", q, len(got), len(want))
		}
		for h := range want {
			if !got[h] {
				t.Fatalf("查询 %d：引擎漏掉了 %v", q, h)
			}
		}
	}
}

func TestOverlapFilterAndHitInfo(t *testing.T) {
	e := NewEngine(EngineOptions{})
	a := e.Add(Capsule{A: Vec3{X: 0}, B: Vec3{X: 0, Y: 1.8}, R: 0.35})
	b := e.Add(Sphere{C: Vec3{X: 0.8, Y: 1}, R: 0.4})
	c := e.Add(Sphere{C: Vec3{X: 9, Y: 1}, R: 0.4})
	_ = c

	query := Sphere{C: Vec3{X: 0.4, Y: 1}, R: 0.6}
	var hits []Result
	e.Overlap(query, func(h Handle) bool { return h != a }, func(r Result) bool {
		hits = append(hits, r)
		return true
	})
	if len(hits) != 1 || hits[0].Handle != b {
		t.Fatalf("过滤后应只剩 b，实际 %+v", hits)
	}
	// 接触点应当落在连接两中心的方向上，深度为正。
	if hits[0].Depth <= 0 || hits[0].Point.IsZero(1e-9) {
		t.Fatalf("命中信息不完整: %+v", hits[0])
	}
	if hits[0].Point.Z != 0 {
		t.Fatalf("接触点应在 x-y 平面内: %v", hits[0].Point)
	}
}

func TestOverlapSector(t *testing.T) {
	// 玩家在原点、朝 +X 挥砍 90°（-45°~+45°），射程 3，内圈 0.5。
	e := NewEngine(EngineOptions{})
	player := e.Add(Capsule{A: Vec3{}, B: Vec3{Y: 1.8}, R: 0.35})
	at := func(x, z float64) Handle {
		return e.Add(Capsule{
			A: Vec3{X: x, Z: z},
			B: Vec3{X: x, Y: 1.8, Z: z},
			R: 0.4,
		})
	}
	front := at(2, 0)      // 正前方 → 命中
	diag := at(2, 1.6)     // 约 39° → 命中
	side := at(0, 2)       // 90° → 不命中
	behind := at(-2, 0)    // 背后 → 不命中
	tooFar := at(6, 0)     // 超出射程 → 不命中
	tooClose := at(0.2, 0) // 内圈之内 → 不命中

	sector := Sector{Center: Vec3{}, From: -math.Pi / 4, To: math.Pi / 4, R0: 0.5, R1: 3}
	got := map[Handle]Result{}
	e.OverlapSector(sector, func(h Handle) bool { return h != player }, func(r Result) bool {
		got[r.Handle] = r
		return true
	})
	want := []Handle{front, diag}
	for _, h := range want {
		if _, ok := got[h]; !ok {
			t.Fatalf("扇区应命中 %v", h)
		}
	}
	for _, h := range []Handle{side, behind, tooFar, tooClose, player} {
		if _, ok := got[h]; ok {
			t.Fatalf("扇区不应命中 %v", h)
		}
	}
	// 命中点必须在目标表面上（离目标中轴恰好一个半径）。
	hit := got[front]
	d := math.Hypot(hit.Point.X-2, hit.Point.Z-0)
	if math.Abs(d-0.4) > 1e-9 {
		t.Fatalf("命中点应在目标表面（离轴 0.4），实际 %v", d)
	}
	// 法向从扇心指向命中点＝击退方向（远离玩家）。
	if hit.Normal.X <= 0 {
		t.Fatalf("击退方向应指离玩家，实际 %v", hit.Normal)
	}
}

func TestRaycastAndSweepHit(t *testing.T) {
	e := NewEngine(EngineOptions{})
	near := e.Add(AABB{Min: Vec3{X: 3, Y: -1, Z: -1}, Max: Vec3{X: 4, Y: 1, Z: 1}})
	far := e.Add(AABB{Min: Vec3{X: 8, Y: -1, Z: -1}, Max: Vec3{X: 9, Y: 1, Z: 1}})

	r, ok := e.Raycast(Vec3{}, Vec3{X: 1}, 20, nil)
	if !ok || r.Handle != near {
		t.Fatalf("射线应命中近的那个，实际 %+v ok=%v", r, ok)
	}
	if math.Abs(r.Dist-3) > 1e-9 {
		t.Fatalf("命中距离应为 3，实际 %v", r.Dist)
	}
	all := e.RaycastAll(Vec3{}, Vec3{X: 1}, 20, nil)
	if len(all) != 2 || all[0].Handle != near || all[1].Handle != far {
		t.Fatalf("RaycastAll 应按距离排序，实际 %+v", all)
	}
	// 过滤器排除近的
	r, ok = e.Raycast(Vec3{}, Vec3{X: 1}, 20, func(h Handle) bool { return h != near })
	if !ok || r.Handle != far {
		t.Fatalf("过滤后应命中远的，实际 %+v", r)
	}

	// 扫掠：球从原点沿 +X 走 20 米，先撞近的。
	hit, ok := e.SweepHit(Sphere{C: Vec3{}, R: 0.5}, Vec3{X: 20}, nil)
	if !ok || hit.Handle != near {
		t.Fatalf("扫掠应命中近的，实际 %+v", hit)
	}
	if math.Abs(hit.Dist-2.5) > 1e-6 {
		t.Fatalf("扫掠距离应为 2.5，实际 %v", hit.Dist)
	}
}

// TestScannerDifferential：增删改混合后，数组扫描器的 Query 仍与暴力一致。
func TestScannerDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	sc := NewArrayScanner()
	boxes := map[Handle]AABB{}
	var next Handle
	add := func() Handle {
		next++
		h := next
		b := AABB{
			Min: Vec3{X: rng.Float64() * 100, Y: 0, Z: rng.Float64() * 100},
		}
		b.Max = b.Min.Add(Vec3{X: 2, Y: 2, Z: 2})
		boxes[h] = b
		sc.Insert(h, b)
		return h
	}
	var live []Handle
	for i := 0; i < 200; i++ {
		live = append(live, add())
	}
	// 混合操作
	for i := 0; i < 200; i++ {
		switch rng.Intn(3) {
		case 0:
			live = append(live, add())
		case 1:
			if len(live) > 0 {
				k := rng.Intn(len(live))
				h := live[k]
				live = append(live[:k], live[k+1:]...)
				sc.Remove(h)
				delete(boxes, h)
			}
		default:
			if len(live) > 0 {
				h := live[rng.Intn(len(live))]
				b := boxes[h]
				b.Translate(Vec3{X: rng.Float64()*4 - 2, Z: rng.Float64()*4 - 2})
				boxes[h] = b
				sc.Update(h, b)
			}
		}
	}
	if sc.Len() != len(boxes) {
		t.Fatalf("扫描器里 %d 个代理，期望 %d 个", sc.Len(), len(boxes))
	}
	for q := 0; q < 200; q++ {
		query := AABB{}
		query.Min = Vec3{X: rng.Float64() * 100, Z: rng.Float64() * 100}
		query.Max = query.Min.Add(Vec3{X: 5, Y: 5, Z: 5})
		want := map[Handle]bool{}
		for h, b := range boxes {
			if TestAABBAABB(query, b) {
				want[h] = true
			}
		}
		got := map[Handle]bool{}
		sc.Query(query, func(h Handle) bool {
			if got[h] {
				t.Fatalf("同一次 Query 里句柄 %v 出现了两次（去重没做好）", h)
			}
			got[h] = true
			return true
		})
		if len(got) != len(want) {
			t.Fatalf("查询 %d：扫描器 %d 个，暴力 %d 个", q, len(got), len(want))
		}
		for h := range want {
			if !got[h] {
				t.Fatalf("查询 %d：扫描器漏掉 %v", q, h)
			}
		}
	}
}

func TestRebuildMatchesIncremental(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	e := NewEngine(EngineOptions{Margin: 0.2})
	for i := 0; i < 100; i++ {
		e.Add(Sphere{
			C: Vec3{X: rng.Float64() * 50, Y: rng.Float64() * 3, Z: rng.Float64() * 50},
			R: 0.5,
		})
	}
	query := AABB{Min: Vec3{X: 10, Y: -1, Z: 10}, Max: Vec3{X: 30, Y: 4, Z: 30}}
	before := collect(e, query)
	e.Rebuild()
	after := collect(e, query)
	if len(before) != len(after) {
		t.Fatalf("Rebuild 改变了查询结果：%d → %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("Rebuild 后候选顺序/集合变化: %v vs %v", before, after)
		}
	}
}

func collect(e *Engine, box AABB) []Handle {
	var out []Handle
	e.Query(box, func(h Handle) bool {
		out = append(out, h)
		return true
	})
	return out
}
