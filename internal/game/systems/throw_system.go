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

// land 结算落地：移除飞行状态，并对落点周围的目标造成伤害。
//
// 伤害**必须走 Attackable.ApplyDamage**（带 thrower 作为 attacker），
// 否则被炸的生物不会记仇、也不会把仇恨传播给同类——
// 表现是"炸了一片怪，没一个理你"。这是很容易踩的静默失效（见
// docs/仇恨传播-组件契约.md 第 6 节）。
func land(w *ecs.World, thrown ecs.Entity, th components.Thrown) {
	// 落点周围的目标（半径内）+ 结算
	hit := blastAround(w, th.ToX, th.ToY, ThrowBlastRadius)
	damage := ThrowBlastDamage

	for _, target := range hit {
		if target == thrown {
			continue // 不炸自己
		}
		if !ecs.Has[components.Attackable](w, target) || !ecs.Has[components.Health](w, target) {
			continue
		}
		// attacker = 投掷者：这样目标会记直接仇恨 + 向同类传播（群体仇恨）。
		// 投掷者已死亡/离线时仍允许结算（炸弹已经飞出去了，不因投手倒下而失效）。
		components.Attackable{}.ApplyDamage(w, target, th.Thrower, damage)
	}

	// 记录爆炸表现（客户端据此画扩散圈）——服务端只发"在哪炸、多大"，
	// 具体特效由客户端负责（职责划分：服务端定范围与作用对象，客户端做表现）。
	components.EmitBlast(w, th.Thrower, thrown, th.ToX, th.ToY, ThrowBlastRadius, damage)

	// 落地即移除飞行状态：投掷物留在落点（后续可被拾取/可堆叠），
	// 这里只清飞行标记，不改它的所有权。
	ecs.Remove[components.Thrown](w, thrown)
}

// blastAround 返回以 (x,y) 为圆心、半径 r 内的可被伤害目标。
//
// 用**欧氏距离**（圆）：爆炸在视觉上是圆的，用方形会出现
// "看着在圈外、却被炸到"的困惑。注意这与 AOI 的方形口径不同，是有意为之。
func blastAround(w *ecs.World, x, y float64, r float64) []ecs.Entity {
	var out []ecs.Entity
	ecs.Query2[components.Position, components.Health](w, func(e ecs.Entity, p *components.Position, hp *components.Health) {
		if hp.Cur <= 0 || !w.IsAlive(e) || ecs.Has[components.Dead](w, e) {
			return
		}
		dx := float64(p.X) - x
		dy := float64(p.Y) - y
		if dx*dx+dy*dy <= r*r {
			out = append(out, e)
		}
	})
	sortEntities(out)
	return out
}

// 投掷落地参数（先用常量；后续可挂到投掷物模板上按物品区分）。
const (
	// ThrowBlastRadius 爆炸半径（格）。
	ThrowBlastRadius = 2.5
	// ThrowBlastDamage 爆炸伤害（点）。
	ThrowBlastDamage = 6
)

// sortEntities 按实体 id 升序排序（确定性）。
func sortEntities(list []ecs.Entity) {
	// 小切片用插入排序：投掷物/命中目标通常只有个位数，避免排序开销。
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j] < list[j-1]; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
