package systems

import (
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// ThrowSystem 推进"飞行中"的投掷物（order 97）：每 tick 累加进度，
// 抵达落点后移除 Thrown 并结算落地效果。
//
// 为什么放在 Move 之后（95）、DebugShape（96）之后：
// 投掷物的位置由轨迹**直接给出**（不参与三阶段移动求解），
// 它不需要避让、也不该被 ORCA 推开。放在移动之后可以确保：
//   - 本 tick 内它不会被 MoveSystem 的错误处理干扰；
//   - 落地结算用的是本 tick 的最终世界状态。
//
// 位置更新方式：把 Position 直接设为插值结果（水平匀速）。
// 高度不参与模拟（只作为表现参数下发客户端），所以"落到指定位置"
// 是天然成立的——不需要解三维弹道。
type ThrowSystem struct{}

func (s *ThrowSystem) Update(w *ecs.World, dt time.Duration) {
	// 收集后再处理：结算过程中可能移除组件/挂 Dead，边遍历边改不安全。
	var flying []ecs.Entity
	ecs.Query[components.Thrown](w, func(e ecs.Entity, _ *components.Thrown) {
		flying = append(flying, e)
	})
	if len(flying) == 0 {
		return
	}
	// 确定性：按实体 id 升序（同 tick 多枚炸弹落地时，结算顺序稳定）。
	sortEntities(flying)

	for _, e := range flying {
		if !w.IsAlive(e) || !ecs.Has[components.Thrown](w, e) {
			continue
		}
		th := ecs.Get[components.Thrown](w, e)
		th.Elapsed++
		x, y := th.CurrentXY()
		// **必须标脏**：Thrown 每 tick 都在变（Elapsed 推进），
		// 而快照只下发"脏组件"。漏了它客户端只会收到创建那一帧的
		// Thrown，之后再也看不到飞行进度（实测：20 tick 的飞行只被
		// 观察到 1 次快照）——与之前 Moveable 只在跨格时标脏是**同一类
		// 契约脱节**：字段在变，但没人告诉同步层。
		ecs.MarkDirty[components.Thrown](w, e)

		// 位置跟随轨迹（整格 + 无子格偏移：飞行是浮点插值，不走 Moveable 的子格语义）。
		if ecs.Has[components.Position](w, e) {
			p := ecs.Get[components.Position](w, e)
			nx, ny := int(x), int(y)
			if p.X != nx || p.Y != ny {
				p.X, p.Y = nx, ny
				ecs.MarkDirty[components.Position](w, e)
			}
		}
		if th.Elapsed >= th.FlightTicks {
			land(w, e, *th)
			continue
		}
	}
}

// land 结算落地：移除飞行状态，并引爆。
//
// 爆炸参数**从实体自己的 Explosive 组件读**（生成时由物品模板拷贝），
// 不在这里写死——这样"炸弹"与"炸药桶"可以共用同一套引爆逻辑，
// 只靠组件参数区分。
//
// 伤害**必须走 Attackable.ApplyDamage**（带 thrower 作为 attacker），
// 否则被炸的生物不会记仇、也不会把仇恨传播给同类——
// 表现是"炸了一片怪，没一个理你"。这是很容易踩的静默失效（见
// docs/仇恨传播-组件契约.md 第 6 节）。
func land(w *ecs.World, thrown ecs.Entity, th components.Thrown) {
	// 没有爆炸属性 = 只是个落地物品，不炸。
	if !ecs.Has[components.Explosive](w, thrown) {
		ecs.Remove[components.Thrown](w, thrown)
		return
	}
	expl := ecs.Get[components.Explosive](w, thrown)
	Detonate(w, thrown, th.Thrower, th.ToX, th.ToY, *expl)
	ecs.Remove[components.Thrown](w, thrown)
}

// Detonate 引爆一个实体：按 Explosive 组件做**半球判定** + 伤害 + 击退 + 广播。
//
// 独立成函数（而不是内联在 land 里）的原因：炸药桶/自爆技能也要引爆，
// 它们不需要"飞行"这段，只共用引爆本身。
//
// 参数 source 是"谁干的"（投掷者/放置者），用于仇恨归属；
// 0 表示无来源（环境爆炸，例如被引燃的油桶），此时不产生仇恨。
func Detonate(
	w *ecs.World,
	explosive ecs.Entity,
	source ecs.Entity,
	centerX, centerY float64,
	expl components.Explosive,
) {
	if expl.Radius <= 0 {
		return
	}
	hits := BlastTargets(w, centerX, centerY, expl.Radius)

	for _, hit := range hits {
		if hit.Entity == explosive {
			continue // 不炸自己
		}
		// 伤害：attacker = 投掷者，这样目标会记直接仇恨 + 向同类传播。
		// 无来源（source=0）时跳过伤害的仇恨部分——ApplyDamage 传 0
		// 不会记仇（AddThreat 的 attacker 为 0 时无意义）。
		if source != 0 &&
			ecs.Has[components.Attackable](w, hit.Entity) &&
			ecs.Has[components.Health](w, hit.Entity) {
			components.Attackable{}.ApplyDamage(w, hit.Entity, source, expl.Damage)
		} else if source == 0 && ecs.Has[components.Health](w, hit.Entity) {
			// 环境爆炸：直接扣血（没有 attacker 可记）
			hp := ecs.Get[components.Health](w, hit.Entity)
			hp.TakeDamage(expl.Damage)
			ecs.MarkDirty[components.Health](w, hit.Entity)
		}
		// 击退：把目标推离爆心（走碰撞滑动，不会推穿墙）
		if expl.Knockback > 0 {
			BlastKnockback(w, centerX, centerY, expl.Radius, expl.Knockback, hit)
		}
	}

	// 广播爆炸（客户端据此画扩散圈/闪光/震屏）。
	// 服务端只给"在哪炸、多大、谁干的"，不约束表现细节。
	components.EmitBlast(w, source, explosive, centerX, centerY, expl.Radius, expl.Damage)
}

// 爆炸参数已改为从实体的 Explosive 组件读（见 components/explosive.go），
// 旧的常量式参数已删除 —— 否则会出现"两处都能配、以哪个为准"的歧义。

// sortEntities 按实体 id 升序排序（确定性）。
func sortEntities(list []ecs.Entity) {
	// 小切片用插入排序：投掷物/命中目标通常只有个位数，避免排序开销。
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
