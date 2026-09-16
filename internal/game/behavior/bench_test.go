package behavior

import (
	"testing"
)

// 本文件在**纯行为树层**测性能：绕开 ECS，用假黑板/假环境，
// 直接量"树遍历 + 节点逻辑"本身的开销。
//
// 与 world/behavior_bench_test.go 的分工：
//   - 这里：树纯逻辑（无 ECS 访问、无寻路），看节点数的边际成本；
//   - 那里：接入 ECS 后的真实开销（含黑板取值、寻路等动作代价）。
//
// 跑法：go test ./internal/game/behavior/ -bench . -benchmem

// benchBoard/benchEnv 是零依赖的替身（见 node_test.go 的 fakeBoard/fakeEnv）。
func newBenchHarness(tree *Tree) (*TickContext, *fakeEnv) {
	env := newFakeEnv()
	board := &fakeBoard{
		target: 99, self: 7, hp: 1000, dmg: 8, rng: 1,
		phase2HP: 200, dist: 1, inRange: true, roam: 8,
	}
	ctx := NewTickContext(board, env, NewMemoryState(), board.self)
	return ctx, env
}

// BenchmarkTreeTick 按树种类测单次 Tick。
//
// 每轮都新建 MemoryState 会掩盖真实开销，所以复用同一个（贴近生产：
// 实体的运行态是常驻组件）。
func BenchmarkTreeTick(b *testing.B) {
	cases := []struct {
		name string
		tree func() *Tree
	}{
		{"dormant5", DormantTree},
		{"prey9", PreyTree},
		{"predator17", PredatorTree},
		{"boss44", func() *Tree { return BossTree(DefaultBossConfig()) }},
	}
	for _, tc := range cases {
		tree := tc.tree()
		ctx, _ := newBenchHarness(tree)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tree.Tick(ctx)
			}
		})
	}
}

// BenchmarkTreeTickWithFreshState 每轮新建运行态（含 map 分配），
// 用于对比"运行态复用 vs 每 tick 重建"的差距。
func BenchmarkTreeTickWithFreshState(b *testing.B) {
	tree := PredatorTree()
	ctx, _ := newBenchHarness(tree)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx.State = NewMemoryState()
		tree.Tick(ctx)
	}
}

// BenchmarkNodeTraversal 只测遍历骨架：一条长子链的 Sequence，
// 用于确认"每次 Tick 的固定成本"（函数调用 + 状态查询）。
func BenchmarkNodeTraversal(b *testing.B) {
	nodes := make([]Node, 0, 16)
	for i := 0; i < 16; i++ {
		nodes = append(nodes, &IdleAction{})
	}
	tree := NewTree(NewSequence(nodes...))
	ctx, _ := newBenchHarness(tree)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.Tick(ctx)
	}
}

// BenchmarkRunningResume 测"停在中间节点续跑"的路径
// （最坏情况：每个组合节点都要查一次运行态）。
func BenchmarkRunningResume(b *testing.B) {
	// scriptedNode 一直返回 Running，逼迫 Sequence 每 tick 存取游标
	mid := &alwaysRunningNode{}
	tree := NewTree(NewSequence(&IdleAction{}, mid, &IdleAction{}))
	ctx, _ := newBenchHarness(tree)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree.Tick(ctx)
	}
}

// alwaysRunningNode 永远返回 Running。
type alwaysRunningNode struct{ nodeBase }

func (n *alwaysRunningNode) Children() []Node                 { return nil }
func (n *alwaysRunningNode) Tick(*TickContext, NodeID) Status { return Running }
