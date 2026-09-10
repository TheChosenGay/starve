package collide

import (
	"math"
	"math/rand"
	"testing"
)

// randBox 造一个随机盒（确定性）。
func randBox(rng *rand.Rand, side float64) AABB {
	c := Vec3{X: rng.Float64() * side, Y: rng.Float64() * 2, Z: rng.Float64() * side}
	e := Vec3{X: 0.2 + rng.Float64(), Y: 0.3 + rng.Float64(), Z: 0.2 + rng.Float64()}
	return AABB{Min: c.Sub(e), Max: c.Add(e)}
}

// TestBVHMatchesBruteForce：增删改混合之后，Query 的候选集合必须与暴力遍历完全一致。
func TestBVHMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20260910))
	tr := NewBVHScanner()
	boxes := map[Handle]AABB{}
	var live []Handle
	var next Handle

	add := func() Handle {
		next++
		b := randBox(rng, 60)
		boxes[next] = b
		live = append(live, next)
		tr.Insert(next, b)
		return next
	}
	for i := 0; i < 400; i++ {
		add()
	}
	for round := 0; round < 600; round++ {
		switch rng.Intn(4) {
		case 0:
			add()
		case 1:
			if len(live) > 0 {
				k := rng.Intn(len(live))
				h := live[k]
				live = append(live[:k], live[k+1:]...)
				tr.Remove(h)
				delete(boxes, h)
			}
		default:
			if len(live) > 0 {
				h := live[rng.Intn(len(live))]
				b := randBox(rng, 60)
				boxes[h] = b
				tr.Update(h, b)
			}
		}
	}
	if tr.Len() != len(boxes) {
		t.Fatalf("树里有 %d 个代理，期望 %d 个", tr.Len(), len(boxes))
	}
	checkBVH(t, tr)

	for q := 0; q < 300; q++ {
		query := randBox(rng, 60)
		want := map[Handle]bool{}
		for h, b := range boxes {
			if TestAABBAABB(query, b) {
				want[h] = true
			}
		}
		got := map[Handle]bool{}
		tr.Query(query, func(h Handle) bool {
			if got[h] {
				t.Fatalf("同一次 Query 里句柄 %v 出现两次", h)
			}
			got[h] = true
			return true
		})
		if len(got) != len(want) {
			t.Fatalf("查询 %d：树给 %d 个候选，暴力 %d 个", q, len(got), len(want))
		}
		for h := range want {
			if !got[h] {
				t.Fatalf("查询 %d：树漏掉 %v", q, h)
			}
		}
	}
}

// TestBVHEarlyExit：回调返回 false 必须立刻停下。
func TestBVHEarlyExit(t *testing.T) {
	tr := NewBVHScanner()
	for i := 0; i < 50; i++ {
		tr.Insert(Handle(i+1), AABB{Min: Vec3{X: float64(i)}, Max: Vec3{X: float64(i) + 1}})
	}
	n := 0
	tr.Query(AABB{Min: Vec3{X: -1, Y: -1, Z: -1}, Max: Vec3{X: 100, Y: 1, Z: 1}}, func(Handle) bool {
		n++
		return false
	})
	if n != 1 {
		t.Fatalf("提前结束应当只回调一次，实际 %d 次", n)
	}
}

// TestBVHBuildMatchesInsert：全量重建与逐个插入必须给出同样的候选集合。
func TestBVHBuildMatchesInsert(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	var hs []Handle
	var boxes []AABB
	for i := 0; i < 500; i++ {
		hs = append(hs, Handle(i+1))
		boxes = append(boxes, randBox(rng, 40))
	}
	incremental := NewBVHScanner()
	for i := range hs {
		incremental.Insert(hs[i], boxes[i])
	}
	bulk := NewBVHScanner()
	bulk.Build(hs, boxes)
	checkBVH(t, bulk)

	if bulk.Height() > incremental.Height() {
		t.Fatalf("bulk build 的树应当不比逐个插入更差：%d vs %d", bulk.Height(), incremental.Height())
	}
	t.Logf("500 个代理：逐个插入树高 %d，bulk build 树高 %d",
		incremental.Height(), bulk.Height())

	for q := 0; q < 200; q++ {
		query := randBox(rng, 40)
		a := collectQuery(incremental, query)
		b := collectQuery(bulk, query)
		if len(a) != len(b) {
			t.Fatalf("查询 %d：增量 %d 个，bulk %d 个", q, len(a), len(b))
		}
		for h := range a {
			if !b[h] {
				t.Fatalf("查询 %d：bulk 漏掉 %v", q, h)
			}
		}
	}
}

// TestBVHUpdateKeepsFatBox：新盒还在旧盒里时，Update 不该动树。
func TestBVHUpdateKeepsFatBox(t *testing.T) {
	tr := NewBVHScanner()
	outer := AABB{Min: Vec3{}, Max: Vec3{X: 10, Y: 10, Z: 10}}
	tr.Insert(1, outer)
	before := tr.nodes[tr.index[1]]
	tr.Update(1, AABB{Min: Vec3{X: 1, Y: 1, Z: 1}, Max: Vec3{X: 2, Y: 2, Z: 2}})
	after := tr.nodes[tr.index[1]]
	if before != after {
		t.Fatal("新盒仍在旧盒内时不该改节点")
	}
	tr.Update(1, AABB{Min: Vec3{X: 20}, Max: Vec3{X: 21, Y: 1, Z: 1}})
	if got := tr.nodes[tr.index[1]].box; got.Max.X < 21 {
		t.Fatalf("逃出旧盒后应当重插，实际 %v", got)
	}
	checkBVH(t, tr)
}

// TestEngineScannerImplementationsAgree：同一个世界分别用数组扫描器与 BVH 扫描器，
// 语义化查询的结果必须一致（这是"换实现不改行为"的契约）。
func TestEngineScannerImplementationsAgree(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	arr := NewEngine(EngineOptions{Margin: 0.25, Scanner: NewArrayScanner()})
	bvh := NewEngine(EngineOptions{Margin: 0.25, Scanner: NewBVHScanner()})
	const n = 400
	arr.Reserve(n)
	bvh.Reserve(n)
	for i := 0; i < n; i++ {
		x, z := rng.Float64()*60-30, rng.Float64()*60-30
		body := Capsule{A: Vec3{X: x, Z: z}, B: Vec3{X: x, Y: 1.8, Z: z}, R: 0.4}
		arr.Add(body)
		bvh.Add(body)
	}
	bvh.Rebuild() // 树用 bulk build，数组扫描器重填一遍

	for q := 0; q < 200; q++ {
		query := Sphere{
			C: Vec3{X: rng.Float64()*60 - 30, Y: 0.9, Z: rng.Float64()*60 - 30},
			R: 1 + rng.Float64()*2,
		}
		a := map[Handle]bool{}
		arr.Overlap(query, nil, func(r Result) bool { a[r.Handle] = true; return true })
		b := map[Handle]bool{}
		bvh.Overlap(query, nil, func(r Result) bool { b[r.Handle] = true; return true })
		if len(a) != len(b) {
			t.Fatalf("查询 %d：数组 %d 个命中，BVH %d 个", q, len(a), len(b))
		}
		for h := range a {
			if !b[h] {
				t.Fatalf("查询 %d：BVH 漏掉 %v", q, h)
			}
		}
	}
	// 扇形与射线也各抽查一次（它们走的是同一条 Query 路径）
	sec := Sector{Center: Vec3{}, From: -1, To: 1, R0: 0.5, R1: 20, Thickness: 5}
	ca := map[Handle]bool{}
	arr.OverlapSector(sec, nil, func(r Result) bool { ca[r.Handle] = true; return true })
	cb := map[Handle]bool{}
	bvh.OverlapSector(sec, nil, func(r Result) bool { cb[r.Handle] = true; return true })
	if len(ca) != len(cb) {
		t.Fatalf("扇形命中数不同：数组 %d，BVH %d", len(ca), len(cb))
	}
	// 注：这里不查射线——世界里的图元是胶囊，而 IntersectRayShape 目前不支持
	// 射线×胶囊（README 的已知缺口第 1 条），会按约定 panic。
}

// checkBVH 校验树的结构不变量：父子指针自洽、内部节点的盒等于孩子的并、高度正确、
// 没有环、叶子数与计数一致。这些是"树看起来能跑但查询悄悄漏东西"的唯一防线。
func checkBVH(t *testing.T, tr *BVHScanner) {
	t.Helper()
	if tr.count != len(tr.index) {
		t.Fatalf("计数不一致：count=%d index=%d", tr.count, len(tr.index))
	}
	if tr.root == bvhNull {
		if tr.count != 0 {
			t.Fatalf("空树却有 %d 个代理", tr.count)
		}
		return
	}
	if tr.nodes[tr.root].parent != bvhNull {
		t.Fatal("根节点的 parent 应为空")
	}
	seen := map[int32]bool{}
	leaves := 0
	var walk func(i int32) int32
	walk = func(i int32) int32 {
		if seen[i] {
			t.Fatalf("节点 %d 被访问两次（树有环或复用出错）", i)
		}
		seen[i] = true
		n := tr.nodes[i]
		if n.leaf {
			leaves++
			if n.height != 0 {
				t.Fatalf("叶子 %d 的高度应为 0，实际 %d", i, n.height)
			}
			if _, ok := tr.index[n.handle]; !ok {
				t.Fatalf("叶子 %d 的句柄 %v 不在索引里", i, n.handle)
			}
			return 0
		}
		c0, c1 := n.child[0], n.child[1]
		if c0 == bvhNull || c1 == bvhNull {
			t.Fatalf("内部节点 %d 缺孩子", i)
		}
		if tr.nodes[c0].parent != i || tr.nodes[c1].parent != i {
			t.Fatalf("节点 %d 的孩子 parent 指针不对", i)
		}
		h0, h1 := walk(c0), walk(c1)
		want := 1 + maxInt32(h0, h1)
		if n.height != want {
			t.Fatalf("节点 %d 的高度应为 %d，实际 %d", i, want, n.height)
		}
		wantBox := tr.nodes[c0].box.Union(tr.nodes[c1].box)
		if !vecAlmostEq(n.box.Min, wantBox.Min) || !vecAlmostEq(n.box.Max, wantBox.Max) {
			t.Fatalf("节点 %d 的盒应为孩子的并：%v..%v vs %v..%v",
				i, n.box.Min, n.box.Max, wantBox.Min, wantBox.Max)
		}
		return want
	}
	walk(tr.root)
	if leaves != tr.count {
		t.Fatalf("叶子数 %d 与代理数 %d 不一致", leaves, tr.count)
	}
	// 树高应当是对数级；退化成链说明平衡没生效
	if maxH := int(math.Ceil(3 * math.Log2(float64(tr.count)+1))); tr.Height() > maxH {
		t.Fatalf("树高 %d 超出对数级上界 %d（树退化了）", tr.Height(), maxH)
	}
}

func collectQuery(tr *BVHScanner, q AABB) map[Handle]bool {
	out := map[Handle]bool{}
	tr.Query(q, func(h Handle) bool { out[h] = true; return true })
	return out
}

var _ Scanner = (*BVHScanner)(nil)
var _ Scanner = (*ArrayScanner)(nil)

// TestScannerBuildContract：两种实现都要满足 Build 的契约（接口自带 Query，直接调）。
func TestScannerBuildContract(t *testing.T) {
	hs := []Handle{1, 2}
	boxes := []AABB{
		{Min: Vec3{}, Max: Vec3{X: 1, Y: 1, Z: 1}},
		{Min: Vec3{X: 5}, Max: Vec3{X: 6, Y: 1, Z: 1}},
	}
	for _, sc := range []Scanner{NewArrayScanner(), NewBVHScanner()} {
		sc.Build(hs, boxes)
		if sc.Len() != 2 {
			t.Fatalf("%T: Build 后应有 2 个代理，实际 %d", sc, sc.Len())
		}
		got := map[Handle]bool{}
		sc.Query(boxes[1], func(h Handle) bool { got[h] = true; return true })
		if len(got) != 1 || !got[2] {
			t.Fatalf("%T: 查询第二个盒应只命中句柄 2，实际 %v", sc, got)
		}
		sc.Remove(2)
		if sc.Len() != 1 {
			t.Fatalf("%T: Remove 后应有 1 个代理，实际 %d", sc, sc.Len())
		}
		got = map[Handle]bool{}
		sc.Query(boxes[1], func(h Handle) bool { got[h] = true; return true })
		if len(got) != 0 {
			t.Fatalf("%T: Remove 后不该再命中，实际 %v", sc, got)
		}
	}
}
