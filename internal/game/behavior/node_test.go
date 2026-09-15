package behavior

import "testing"

// --- 测试替身：可控黑板 / 环境 ---

type fakeBoard struct {
	target             uint64
	self               uint64
	hp, hpMax, fleeHP  int
	dmg, rng, cd       int
	inRange, sees      bool
	roam, homeX, homeY int
	now                int
}

func (b *fakeBoard) Target() uint64           { return b.target }
func (b *fakeBoard) SetTarget(e uint64)       { b.target = e }
func (b *fakeBoard) Self() uint64             { return b.self }
func (b *fakeBoard) Health() int              { return b.hp }
func (b *fakeBoard) HealthMax() int           { return b.hpMax }
func (b *fakeBoard) FleeHP() int              { return b.fleeHP }
func (b *fakeBoard) AttackDamage() int        { return b.dmg }
func (b *fakeBoard) AttackRange() int         { return b.rng }
func (b *fakeBoard) AttackCooldownTicks() int { return b.cd }
func (b *fakeBoard) InAttackRange() bool      { return b.inRange }
func (b *fakeBoard) CanSee(uint64) bool       { return b.sees }
func (b *fakeBoard) RoamRadius() int          { return b.roam }
func (b *fakeBoard) HomeX() int               { return b.homeX }
func (b *fakeBoard) HomeY() int               { return b.homeY }
func (b *fakeBoard) Now() int                 { return b.now }

// fakeEnv 记录动作节点提交了哪些意图。
type fakeEnv struct {
	moves     [][2]int
	toward    []uint64
	fled      []uint64
	homeCount int
	attacks   []uint64
	ready     bool
	homeDist  int
	randVal   int
}

func newFakeEnv() *fakeEnv { return &fakeEnv{ready: true} }

func (e *fakeEnv) Rand(n int) int {
	if n <= 0 {
		return 0
	}
	return e.randVal % n
}
func (e *fakeEnv) MoveDir(dx, dy int)   { e.moves = append(e.moves, [2]int{dx, dy}) }
func (e *fakeEnv) MovePath([]MoveStep)  {}
func (e *fakeEnv) MoveToward(t uint64)  { e.toward = append(e.toward, t) }
func (e *fakeEnv) FleeFrom(t uint64)    { e.fled = append(e.fled, t) }
func (e *fakeEnv) MoveHome()            { e.homeCount++ }
func (e *fakeEnv) StartAttack(t uint64) { e.attacks = append(e.attacks, t) }
func (e *fakeEnv) AttackReady() bool    { return e.ready }
func (e *fakeEnv) HomeDistance() int    { return e.homeDist }

// harness 把树 + 黑板 + 环境 + 运行态绑在一起，方便逐 tick 驱动。
type harness struct {
	tree  *Tree
	board *fakeBoard
	env   *fakeEnv
	state *MemoryState
}

func newHarness(t *testing.T, tree *Tree, b *fakeBoard, e *fakeEnv) *harness {
	t.Helper()
	return &harness{tree: tree, board: b, env: e, state: NewMemoryState()}
}

func (h *harness) tick() Status {
	return h.tree.Tick(NewTickContext(h.board, h.env, h.state, h.board.self))
}

// --- 组合节点语义 ---

func TestSelectorShortCircuitsOnSuccess(t *testing.T) {
	// 第一个成功 → 第二个不该执行。
	second := &countingNode{}
	sel := NewSelector(&LowHP{}, second)
	tree := NewTree(sel)
	b := &fakeBoard{hp: 10, fleeHP: 20}
	h := newHarness(t, tree, b, newFakeEnv())

	if got := h.tick(); got != Success {
		t.Fatalf("Selector 应在 LowHP 成功时返回 Success，得到 %v", got)
	}
	if second.ticks != 0 {
		t.Fatalf("短路失效：第二个节点被执行了 %d 次", second.ticks)
	}
}

func TestSelectorFailsWhenAllChildrenFail(t *testing.T) {
	tree := NewTree(NewSelector(&HasTarget{}, &LowHP{}))
	b := &fakeBoard{target: 0, fleeHP: 0}
	h := newHarness(t, tree, b, newFakeEnv())
	if got := h.tick(); got != Failure {
		t.Fatalf("全部子节点失败应返回 Failure，得到 %v", got)
	}
}

func TestSequenceStopsOnFailure(t *testing.T) {
	second := &countingNode{}
	seq := NewSequence(&HasTarget{}, second)
	tree := NewTree(seq)
	b := &fakeBoard{target: 0} // HasTarget 失败
	h := newHarness(t, tree, b, newFakeEnv())

	if got := h.tick(); got != Failure {
		t.Fatalf("Sequence 首节点失败应返回 Failure，得到 %v", got)
	}
	if second.ticks != 0 {
		t.Fatalf("Sequence 失败后不应继续执行后续节点，实际执行 %d 次", second.ticks)
	}
}

func TestSequenceAllSuccess(t *testing.T) {
	tree := NewTree(NewSequence(&HasTarget{}, &InAttackRange{}))
	b := &fakeBoard{target: 7, inRange: true}
	h := newHarness(t, tree, b, newFakeEnv())
	if got := h.tick(); got != Success {
		t.Fatalf("全部成功应返回 Success，得到 %v", got)
	}
}

// --- Running 续跑语义（行为树的核心） ---

func TestSequenceResumesFromRunningChild(t *testing.T) {
	// 第一个子节点第一次 Running，第二次 Success；第二个节点应只在
	// 第一个成功之后才被执行——且不应重复执行第一个节点。
	first := &scriptedNode{results: []Status{Running, Success, Success}}
	second := &countingNode{}
	tree := NewTree(NewSequence(first, second))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())

	if got := h.tick(); got != Running {
		t.Fatalf("第一次应为 Running，得到 %v", got)
	}
	if second.ticks != 0 {
		t.Fatalf("Running 时不该推进到第二个节点")
	}
	if got := h.tick(); got != Success {
		t.Fatalf("第二次应续跑并成功，得到 %v", got)
	}
	if first.ticks != 2 {
		t.Fatalf("第一个节点应执行 2 次（Running → Success），实际 %d", first.ticks)
	}
	if second.ticks != 1 {
		t.Fatalf("第二个节点应执行 1 次，实际 %d", second.ticks)
	}
}

func TestSelectorResumesFromRunningChild(t *testing.T) {
	// 选择器：第一个 Running → 下 tick 从它续跑，而不是重试从头。
	first := &scriptedNode{results: []Status{Running, Success}}
	second := &countingNode{}
	tree := NewTree(NewSelector(first, second))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())

	h.tick()
	h.tick()
	if first.ticks != 2 {
		t.Fatalf("应续跑第一个节点，实际执行 %d 次", first.ticks)
	}
	if second.ticks != 0 {
		t.Fatalf("第一个节点成功时应短路，第二个执行了 %d 次", second.ticks)
	}
}

func TestRunningCursorClearedAfterCompletion(t *testing.T) {
	// 一轮跑完后必须清掉游标，否则下一轮会从中间某处开始（经典 bug）。
	first := &scriptedNode{results: []Status{Running, Success, Failure, Success}}
	tree := NewTree(NewSequence(first, &countingNode{}))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())

	h.tick() // Running
	h.tick() // Success，游标应清空

	if _, ok := h.state.RunningChildOf(idOf(tree.Root())); ok {
		t.Fatalf("Sequence 完成后应清除 Running 游标")
	}
}

// --- 装饰节点 ---

func TestInverter(t *testing.T) {
	tree := NewTree(NewInverter(&HasTarget{}))
	b := &fakeBoard{target: 0}
	h := newHarness(t, tree, b, newFakeEnv())
	if got := h.tick(); got != Success {
		t.Fatalf("Inverter(失败) 应为 Success，得到 %v", got)
	}
	b.target = 5
	if got := h.tick(); got != Failure {
		t.Fatalf("Inverter(成功) 应为 Failure，得到 %v", got)
	}
}

func TestSucceederAlwaysSucceeds(t *testing.T) {
	tree := NewTree(NewSucceeder(&HasTarget{}))
	h := newHarness(t, tree, &fakeBoard{target: 0}, newFakeEnv())
	if got := h.tick(); got != Success {
		t.Fatalf("Succeeder 应恒为 Success，得到 %v", got)
	}
}

func TestCooldownBlocksThenAllows(t *testing.T) {
	// 冷却 2 tick：第一次放行，随后 2 tick 被挡，第 4 次再放行。
	inner := &countingNode{}
	tree := NewTree(NewCooldown(2, inner))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())

	got := []Status{h.tick(), h.tick(), h.tick(), h.tick()}
	want := []Status{Success, Failure, Failure, Success}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 次：得到 %v，期望 %v（全部：%v）", i+1, got[i], want[i], got)
		}
	}
}

func TestCooldownZeroPassesThrough(t *testing.T) {
	inner := &countingNode{}
	tree := NewTree(NewCooldown(0, inner))
	h := newHarness(t, tree, &fakeBoard{}, newFakeEnv())
	h.tick()
	h.tick()
	if inner.ticks != 2 {
		t.Fatalf("冷却 0 应透传每次执行，实际 %d 次", inner.ticks)
	}
}

// --- 构造期校验 ---

func TestNewTreePanicsOnNilRoot(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("nil 根节点应 panic")
		}
	}()
	NewTree(nil)
}

func TestNewTreePanicsOnRepeatedNode(t *testing.T) {
	shared := &countingNode{}
	defer func() {
		if recover() == nil {
			t.Fatalf("同一节点实例挂载两次应 panic（id 会冲突）")
		}
	}()
	NewTree(NewSequence(shared, shared))
}

func TestAssignIDsAreStableAndUnique(t *testing.T) {
	tree := PredatorTree()
	seen := map[NodeID]bool{}
	var walk func(n Node)
	walk = func(n Node) {
		id := idOf(n)
		if seen[id] {
			t.Fatalf("节点 id 重复：%d", id)
		}
		seen[id] = true
		for _, c := range n.Children() {
			walk(c)
		}
	}
	walk(tree.Root())
	if len(seen) != tree.NodeCount() {
		t.Fatalf("id 数量 %d 与节点数 %d 不符", len(seen), tree.NodeCount())
	}

	// 同样方式重建一棵，id 应完全一致（存档兼容的前提）。
	other := PredatorTree()
	var ids1, ids2 []NodeID
	var collect func(n Node, out *[]NodeID)
	collect = func(n Node, out *[]NodeID) {
		*out = append(*out, idOf(n))
		for _, c := range n.Children() {
			collect(c, out)
		}
	}
	collect(tree.Root(), &ids1)
	collect(other.Root(), &ids2)

	if len(ids1) != len(ids2) {
		t.Fatalf("两次构建的节点数不同：%d vs %d", len(ids1), len(ids2))
	}
	for i := range ids1 {
		if ids1[i] != ids2[i] {
			t.Fatalf("第 %d 个节点 id 不稳定：%d vs %d", i, ids1[i], ids2[i])
		}
	}
}

// --- 测试替身实现 ---

// countingNode 永远成功，统计被调用了多少次。
type countingNode struct {
	nodeBase
	ticks int
}

func (n *countingNode) Children() []Node { return nil }
func (n *countingNode) Tick(*TickContext, NodeID) Status {
	n.ticks++
	return Success
}

// scriptedNode 按预设脚本依次返回状态（超出后返回最后一个）。
type scriptedNode struct {
	nodeBase
	results []Status
	ticks   int
}

func (n *scriptedNode) Children() []Node { return nil }
func (n *scriptedNode) Tick(*TickContext, NodeID) Status {
	i := n.ticks
	n.ticks++
	if i >= len(n.results) {
		if len(n.results) == 0 {
			return Failure
		}
		return n.results[len(n.results)-1]
	}
	return n.results[i]
}
