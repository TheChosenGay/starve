package interactive

import (
	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Intent 交互意图（复用 WorkAction：chop/mine/pick；attack 后续接入）。
type Intent = game.WorkAction

const (
	IntentChop   = game.WorkAction_WORK_ACTION_CHOP
	IntentMine   = game.WorkAction_WORK_ACTION_MINE
	IntentPick   = game.WorkAction_WORK_ACTION_PICK
	IntentAttack = game.WorkAction_WORK_ACTION_ATTACK
)

// Pair 一对匹配的（主动 -er ↔ 受激 -able）能力：注册表声明匹配关系。
type Pair struct {
	Intent           Intent
	Active, Reactive ecs.ComponentID // 声明性说明（日志/文档）
	Range            func(w *ecs.World, actor ecs.Entity) (int, bool)
	Match            func(w *ecs.World, actor, target ecs.Entity) bool
	// Apply 执行一次作用，返回结果 + 是否真正生效（已耗尽返回 applied=false）。
	Apply func(w *ecs.World, actor, target ecs.Entity) (DoResult, bool)
}

// DoResult 一次交互的结果（闭环：副作用判断全部来自这里）。
type DoResult struct {
	Kind        game.ItemKind
	Depleted    bool // 目标耗尽（工作量归零）
	ToolBroken  bool // 作用方手持工具耐久耗尽
	DamageDealt int  // 攻击实际造成的伤害（目标 Health 减免后）
}

var pairs = map[Intent]Pair{}

// RegisterPair 注册一对匹配能力（新交互 = 加一对，不改既有逻辑）。
func RegisterPair(p Pair) { pairs[p.Intent] = p }

// RangeOf 作用方对该意图的作用距离；未注册/无主动能力返回 false。
func RangeOf(w *ecs.World, actor ecs.Entity, intent Intent) (int, bool) {
	p, ok := pairs[intent]
	if !ok || p.Range == nil {
		return 0, false
	}
	return p.Range(w, actor)
}

// Do 按意图匹配（主动↔受激）并执行一次交互；类型不匹配、未注册或已耗尽返回 false。
func Do(w *ecs.World, actor, target ecs.Entity, intent Intent) (DoResult, bool) {
	p, ok := pairs[intent]
	if !ok || p.Match == nil || !p.Match(w, actor, target) {
		return DoResult{}, false
	}
	res, applied := p.Apply(w, actor, target)
	if !applied {
		return DoResult{}, false
	}
	return res, true
}

// handTool 作用方手持槽位的工具实体（0 = 空手）。
func handTool(w *ecs.World, actor ecs.Entity) ecs.Entity {
	if !ecs.Has[Equip](w, actor) {
		return 0
	}
	return ecs.Get[Equip](w, actor).Item(SlotHand)
}

// WorkDepleted 目标任一受激工作能力是否耗尽。
func WorkDepleted(w *ecs.World, e ecs.Entity) bool {
	if ecs.Has[Choppable](w, e) {
		return ecs.Get[Choppable](w, e).WorkLeft <= 0
	}
	if ecs.Has[Minable](w, e) {
		return ecs.Get[Minable](w, e).WorkLeft <= 0
	}
	if ecs.Has[Pickable](w, e) {
		return ecs.Get[Pickable](w, e).WorkLeft <= 0
	}
	return false
}

// RestoreWork 恢复目标受激工作能力到 MaxWork（重生到点调用）。
func RestoreWork(w *ecs.World, e ecs.Entity) {
	if ecs.Has[Choppable](w, e) {
		c := ecs.Get[Choppable](w, e)
		c.WorkLeft = c.MaxWork
		ecs.MarkDirty[Choppable](w, e)
	}
	if ecs.Has[Minable](w, e) {
		c := ecs.Get[Minable](w, e)
		c.WorkLeft = c.MaxWork
		ecs.MarkDirty[Minable](w, e)
	}
	if ecs.Has[Pickable](w, e) {
		c := ecs.Get[Pickable](w, e)
		c.WorkLeft = c.MaxWork
		ecs.MarkDirty[Pickable](w, e)
	}
}

// RegisterWork 统一注册"工作量型"交互（砍/挖/采共用一套逻辑）：
// 组件保持纯数据，泛型通过传入的字段读写闭包访问（不挂方法/接口）；
// active 返回（效率, 距离, 耐久指针），reactive 返回（资源 kind, 工作量指针, 上限指针）。
// 不同流程的交互（如 attack）不经过这里，直接 RegisterPair 单独注册。
func RegisterWork[A, R any](
	intent Intent,
	activeID, reactiveID ecs.ComponentID,
	active func(*A) (eff, rng int, dur *int),
	reactive func(*R) (kind game.ItemKind, left, max *int),
) {
	RegisterPair(Pair{
		Intent: intent, Active: activeID, Reactive: reactiveID,
		Range: func(w *ecs.World, actor ecs.Entity) (int, bool) {
			if !ecs.Has[A](w, actor) {
				return 0, false
			}
			_, rng, _ := active(ecs.Get[A](w, actor))
			return rng, true
		},
		Match: func(w *ecs.World, actor, target ecs.Entity) bool {
			return ecs.Has[A](w, actor) && ecs.Has[R](w, target)
		},
		Apply: func(w *ecs.World, actor, target ecs.Entity) (DoResult, bool) {
			eff, _, _ := active(ecs.Get[A](w, actor))
			kind, left, _ := reactive(ecs.Get[R](w, target))
			if *left <= 0 {
				return DoResult{Kind: kind}, false
			}
			*left -= eff
			depleted := *left <= 0
			if depleted {
				*left = 0
			}
			ecs.MarkDirty[R](w, target)
			res := DoResult{Kind: kind, Depleted: depleted}
			if tool := handTool(w, actor); tool != 0 && ecs.Has[A](w, tool) {
				_, _, tdur := active(ecs.Get[A](w, tool))
				*tdur--
				ecs.MarkDirty[A](w, tool)
				if *tdur <= 0 {
					res.ToolBroken = true
				}
			}
			return res, true
		},
	})
}

func init() {
	RegisterWork(IntentChop, "Chopper", "Choppable",
		func(c *Chopper) (int, int, *int) { return c.Efficiency, c.Range, &c.Durability },
		func(t *Choppable) (game.ItemKind, *int, *int) { return t.Kind, &t.WorkLeft, &t.MaxWork },
	)
	RegisterWork(IntentMine, "Miner", "Minable",
		func(c *Miner) (int, int, *int) { return c.Efficiency, c.Range, &c.Durability },
		func(t *Minable) (game.ItemKind, *int, *int) { return t.Kind, &t.WorkLeft, &t.MaxWork },
	)
	RegisterWork(IntentPick, "Picker", "Pickable",
		func(c *Picker) (int, int, *int) { return c.Efficiency, c.Range, &c.Durability },
		func(t *Pickable) (game.ItemKind, *int, *int) { return t.Kind, &t.WorkLeft, &t.MaxWork },
	)
}
