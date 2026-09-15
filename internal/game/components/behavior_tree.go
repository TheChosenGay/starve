package components

import (
	"sort"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/behavior"
	game "starve/pkg/proto/game"
)

// BehaviorTree 是挂在实体上的行为树运行态组件。
//
// 分工：
//   - **树定义**（结构）不存进组件——它是静态的、可被所有同类实体共享的
//     （见 behavior.PredatorTree / PreyTree）。组件里只存一个 Kind 用于
//     取回定义，以及**每个实体各自的运行态**（Running 游标 + 计数器）。
//   - 运行态必须进快照/存档：否则读档后 AI 会从树根重新决策，行为突变。
//
// TreeKind 用枚举而非指针，正是为了可序列化（指针跨进程无意义）。
type BehaviorTree struct {
	// Kind 选用哪棵内置树（见 behavior 包）。
	Kind BehaviorTreeKind
	// RunningChild 记录"某个组合节点正在跑第几个子节点"（节点 id → 下标）。
	//
	// 只存组合节点的游标：叶子节点不需要（它们要么瞬时、要么由 Cooldown 计时）。
	RunningChild map[uint32]uint8
	// Counters 是节点级计数器（当前只有 Cooldown 用：剩余冷却 tick）。
	Counters map[uint32]int
}

// BehaviorTreeKind 内置行为树种类（与 behavior 包工厂一一对应）。
type BehaviorTreeKind = game.BehaviorTreeKind

const (
	TreeKindUnspecified = game.BehaviorTreeKind_BEHAVIOR_TREE_KIND_UNSPECIFIED
	TreeKindPredator    = game.BehaviorTreeKind_BEHAVIOR_TREE_KIND_PREDATOR
	TreeKindPrey        = game.BehaviorTreeKind_BEHAVIOR_TREE_KIND_PREY
	TreeKindDormant     = game.BehaviorTreeKind_BEHAVIOR_TREE_KIND_DORMANT
	TreeKindBoss        = game.BehaviorTreeKind_BEHAVIOR_TREE_KIND_BOSS
)

// TreeKindByName 配置字符串 → 树种类。
var TreeKindByName = map[string]BehaviorTreeKind{
	"predator": TreeKindPredator,
	"prey":     TreeKindPrey,
	"dormant":  TreeKindDormant,
	"boss":     TreeKindBoss,
}

// BossConfigOf 是 Boss 树的可调参数（用默认值；需要按实体调参时再挂组件）。
func bossConfig() behavior.BossConfig { return behavior.DefaultBossConfig() }

// TreeOf 按 Kind 返回内置树定义；未知 kind 返回 nil。
//
// 每次调用都新建一棵（节点 id 会重新分配，但**分配顺序固定**所以 id 稳定）。
// 树定义不可跨实体共享实例（节点自带 id），需要复用时由调用方缓存
// （systems 包按 kind 缓存，避免每 tick 重建整棵树）。
func TreeOf(k BehaviorTreeKind) *behavior.Tree {
	switch k {
	case TreeKindPredator:
		return behavior.PredatorTree()
	case TreeKindPrey:
		return behavior.PreyTree()
	case TreeKindDormant:
		return behavior.DormantTree()
	case TreeKindBoss:
		return behavior.BossTree(bossConfig())
	}
	return nil
}

// TreeKindForTemplate 按生物的"能否攻击"选缺省树（配置未显式指定时用）。
func TreeKindForTemplate(canAttack bool) BehaviorTreeKind {
	if canAttack {
		return TreeKindPredator
	}
	return TreeKindPrey
}

// --- NodeStateStore 实现（组件即运行态存储）---

// RunningChildOf 取组合节点 id 的游标（实现 behavior.NodeStateStore）。
func (b *BehaviorTree) RunningChildOf(id behavior.NodeID) (uint8, bool) {
	v, ok := b.RunningChild[uint32(id)]
	return v, ok
}

// SetRunningChildOf 写游标。
func (b *BehaviorTree) SetRunningChildOf(id behavior.NodeID, idx uint8) {
	if b.RunningChild == nil {
		b.RunningChild = map[uint32]uint8{}
	}
	b.RunningChild[uint32(id)] = idx
}

// ClearRunningChildOf 清游标。
func (b *BehaviorTree) ClearRunningChildOf(id behavior.NodeID) {
	delete(b.RunningChild, uint32(id))
}

// IntOf 取节点计数器。
func (b *BehaviorTree) IntOf(id behavior.NodeID) int { return b.Counters[uint32(id)] }

// SetIntOf 写节点计数器。
func (b *BehaviorTree) SetIntOf(id behavior.NodeID, v int) {
	if b.Counters == nil {
		b.Counters = map[uint32]int{}
	}
	b.Counters[uint32(id)] = v
}

// ClearIntOf 删节点计数器。
func (b *BehaviorTree) ClearIntOf(id behavior.NodeID) { delete(b.Counters, uint32(id)) }

// 断言：组件满足行为树的运行态存储接口。
var _ behavior.NodeStateStore = (*BehaviorTree)(nil)

// --- 快照/存档编解码 ---

type behaviorTreeCodec struct{}

func (behaviorTreeCodec) Encode(v BehaviorTree) ([]byte, error) {
	out := &game.BehaviorTree{Kind: v.Kind}
	// 按节点 id 升序编码（map 遍历顺序随机，不排序会导致快照不可复现）
	ids := make([]int, 0, len(v.RunningChild))
	for id := range v.RunningChild {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, id := range ids {
		out.RunningChild = append(out.RunningChild, &game.BTNodeCursor{
			NodeId: uint32(id),
			Child:  int32(v.RunningChild[uint32(id)]),
		})
	}
	cids := make([]int, 0, len(v.Counters))
	for id := range v.Counters {
		cids = append(cids, int(id))
	}
	sort.Ints(cids)
	for _, id := range cids {
		out.Counters = append(out.Counters, &game.BTNodeCounter{
			NodeId: uint32(id),
			Value:  int32(v.Counters[uint32(id)]),
		})
	}
	return pb.Marshal(out)
}

func (behaviorTreeCodec) Decode(b []byte) (BehaviorTree, error) {
	var m game.BehaviorTree
	if err := pb.Unmarshal(b, &m); err != nil {
		return BehaviorTree{}, err
	}
	out := BehaviorTree{
		Kind:         m.Kind,
		RunningChild: map[uint32]uint8{},
		Counters:     map[uint32]int{},
	}
	for _, c := range m.RunningChild {
		if c != nil {
			out.RunningChild[c.NodeId] = uint8(c.Child)
		}
	}
	for _, c := range m.Counters {
		if c != nil {
			out.Counters[c.NodeId] = int(c.Value)
		}
	}
	return out, nil
}

// RegisterBehaviorTree 注册组件 codec。
func RegisterBehaviorTree(w *ecs.World) {
	ecs.RegisterComponent(w, "BehaviorTree", behaviorTreeCodec{})
}
