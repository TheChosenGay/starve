package systems

import (
	"starve/internal/ecs"
	"starve/internal/game/behavior"
	"starve/internal/game/components"
)

// 本文件管理**行为树定义缓存**与实体 ↔ 树的绑定。
//
// 为什么需要缓存：behavior.Tree 构建时要分配节点 id 并做校验，每 tick
// 每个生物重建一棵是纯浪费（12 只狼 = 每 tick 12 次建树）。树定义是静态的，
// 按 kind 缓存即可；**运行态**仍然在各自的 BehaviorTree 组件里，
// 所以共享同一棵树实例是安全的（节点无状态，id 一致）。

// treeCache 是世界级 Resource：按 kind 缓存已构建的树定义。
type treeCache struct {
	trees map[components.BehaviorTreeKind]*behavior.Tree
}

// TreeCacheResource 返回世界的树缓存（不存在则创建）。
func TreeCacheResource(w *ecs.World) *treeCache {
	if c, ok := ecs.TryResource[treeCache](w); ok {
		return c
	}
	c := &treeCache{trees: map[components.BehaviorTreeKind]*behavior.Tree{}}
	w.AddResource(c)
	return c
}

// treeForIn 在指定世界里取树定义：按 kind 缓存，避免每 tick 重建。
//
// 树定义本身不可变（构建后只读），节点也无可变状态（运行态存在组件的
// BehaviorTree 里），所以多个实体共享同一实例是安全的。
func treeForIn(w *ecs.World, k components.BehaviorTreeKind) *behavior.Tree {
	cache := TreeCacheResource(w)
	if t, ok := cache.trees[k]; ok {
		return t
	}
	t := components.TreeOf(k)
	if t != nil {
		cache.trees[k] = t
	}
	return t
}

// behaviorTreeOf 取实体的行为树组件；没有则返回 nil（调用方回退旧状态机）。
func behaviorTreeOf(w *ecs.World, e ecs.Entity) *components.BehaviorTree {
	if !ecs.Has[components.BehaviorTree](w, e) {
		return nil
	}
	return ecs.Get[components.BehaviorTree](w, e)
}

// EnsureBehaviorTree 给实体挂上行为树组件（kind 为 0 时按"能否攻击"推断）。
//
// 生成生物时调用（seedCreatures），也用于把旧存档实体迁移到行为树。
func EnsureBehaviorTree(w *ecs.World, e ecs.Entity, kind components.BehaviorTreeKind) {
	if kind == components.TreeKindUnspecified {
		kind = components.TreeKindForTemplate(canAttackOf(w, e))
	}
	if ecs.Has[components.BehaviorTree](w, e) {
		bt := ecs.Get[components.BehaviorTree](w, e)
		if bt.Kind != kind {
			bt.Kind = kind
			bt.RunningChild = nil
			bt.Counters = nil
			ecs.MarkDirty[components.BehaviorTree](w, e)
		}
		return
	}
	ecs.Add(w, e, components.BehaviorTree{
		Kind:         kind,
		RunningChild: map[uint32]uint8{},
		Counters:     map[uint32]int{},
	})
}

// canAttackOf 实体是否有攻击能力（决定用掠食者树还是被动树）。
func canAttackOf(w *ecs.World, e ecs.Entity) bool {
	return weaponOf(w, e).AttackDamage > 0
}

// TickBehaviorTree 只驱动一个实体的行为树（不跑感知/仇恨/移动）。
//
// 用途：性能测试里把"行为树本身的开销"从 AISystem 的其余工作中隔离出来
// （见 world/behavior_bench_test.go 的 Isolated 基准）。
// 生产路径仍然走 AISystem.Update —— 那里还负责感知与目标选择。
//
// 返回树是否被真正执行（实体没有 BehaviorTree 组件时返回 false）。
func TickBehaviorTree(w *ecs.World, e ecs.Entity) bool {
	bt := behaviorTreeOf(w, e)
	if bt == nil {
		return false
	}
	tree := treeForIn(w, bt.Kind)
	if tree == nil {
		return false
	}
	board := newBoard(w, e)
	ctx := behavior.NewTickContext(board, newEnv(w, e), bt, uint64(e))
	tree.Tick(ctx)
	return true
}
