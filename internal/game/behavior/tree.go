package behavior

import (
	"fmt"
	"strings"
)

// Tree 是一棵可执行的行为树：根节点 + 已分配的节点 id。
//
// 构建（NewTree）时会做两件事：
//  1. **分配 NodeID**：深度优先、按子节点声明顺序自增——只要树的构建
//     顺序不变，id 就稳定，存档/重放因此可复现；
//  2. **校验**：节点非 nil、无环（检查节点被重复挂载）、装饰器恰好一个子节点。
//
// Tree 本身是**不可变**的（构建后只读），可以被任意多个实体共享；
// 每个实体的运行态存在各自组件的 NodeStateStore 里（id 相同即可对齐）。
type Tree struct {
	root  Node
	nodes int
}

// NewTree 构建一棵树并对整棵树分配 id + 校验。
//
// 节点实例**不可跨树共享**（id 会冲突）——重复挂载同一个节点指针会被
// 校验出来并 panic（这是构建期错误，应当尽早暴露而不是运行时行为诡异）。
func NewTree(root Node) *Tree {
	if root == nil {
		panic("behavior: NewTree: root 不能为 nil")
	}
	t := &Tree{root: root}
	seen := map[Node]bool{}
	t.assignIDs(root, seen)
	return t
}

// assignIDs 深度优先分配 id（自增），同时校验节点合法性。
func (t *Tree) assignIDs(n Node, seen map[Node]bool) {
	if n == nil {
		panic("behavior: 树里存在 nil 子节点（装饰器/组合器收到 nil？）")
	}
	if seen[n] {
		panic(fmt.Sprintf("behavior: 节点被重复挂载（同一实例出现在多处）：%T", n))
	}
	seen[n] = true
	if id, ok := n.(identifier); ok {
		id.identify(NodeID(t.nodes))
	}
	t.nodes++
	for _, c := range n.Children() {
		t.assignIDs(c, seen)
	}
}

// Root 返回根节点。
func (t *Tree) Root() Node { return t.root }

// NodeCount 返回树内节点数（含根）。
func (t *Tree) NodeCount() int { return t.nodes }

// Tick 从根驱动一次整棵树。
//
// 返回根节点的状态。调用方（AISystem）不关心细节，只管每 tick 调一次。
func (t *Tree) Tick(d *TickContext) Status {
	return tickNode(d, t.root)
}

// Reset 清空本次运行留下的所有 Running 游标与计数器。
//
// 用途：AI 被"重置"时（例如重生、或语义上要求重新决策）强制下 tick 从根重走。
// 实现方式是对已知节点类型做一次遍历清理——只清**有状态节点**，
// 条件/纯动作没有状态，跳过。
func (t *Tree) Reset(state NodeStateStore) {
	if state == nil {
		return
	}
	resetNode(t.root, state)
}

func resetNode(n Node, state NodeStateStore) {
	switch v := n.(type) {
	case *Sequence:
		state.ClearRunningChildOf(idOf(v))
	case *Selector:
		state.ClearRunningChildOf(idOf(v))
	case *Cooldown:
		state.ClearIntOf(idOf(v))
	}
	for _, c := range n.Children() {
		resetNode(c, state)
	}
}

// Describe 输出树的结构（调试/测试用，例如 AI 行为不符预期时打印出来看）。
func (t *Tree) Describe() string {
	var b strings.Builder
	describeNode(&b, t.root, 0)
	return b.String()
}

func describeNode(b *strings.Builder, n Node, depth int) {
	b.WriteString(strings.Repeat("  ", depth))
	fmt.Fprintf(b, "%T#%d\n", n, idOf(n))
	for _, c := range n.Children() {
		describeNode(b, c, depth+1)
	}
}
