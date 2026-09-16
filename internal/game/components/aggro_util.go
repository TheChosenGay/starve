package components

import (
	"starve/internal/ecs"
)

// 本文件提供**构造辅助**：把"参与群体仇恨所需的最小组件集"封成一次调用，
// 避免手工逐个 ecs.Add 时漏挂——漏挂的表现是**静默失效**
// （不报错、不崩溃，只是"打了没反应"），是本项目最难排查的一类 bug。
//
// 契约详见 docs/仇恨传播-组件契约.md。
//
// 注意：本文件**不是**要取代 configs/creatures.json + seed.go 的正规路径
// （那条路径还会挂 Collide / DropSource / AI / BehaviorTree 等）。
// 它服务的是另外两类场景：
//   - 测试：造一只"能参与仇恨"的实体，不必重复 5 行
//   - 技能/召唤物等**运行时动态生成**的实体

// AggroCapable 描述"一只可参与群体仇恨的生物"。
//
// 字段都是必备项的来源，故意**不做隐式默认值**（除了坐标）：
// 半径/种类配错会让行为静默反常，显式写出来更容易在 review 时发现。
type AggroCapable struct {
	Kind CreatureKind // 同类判定依据：只有同 Kind 之间才传播
	X, Y int          // 位置（格）

	// HP/MaxHP 生命值。MaxHP 为 0 时按 HP 补齐。
	HP, MaxHP int

	// Perception 感知半径（发现敌人）：小，保留潜行感。
	// Threat 仇恨传播半径（同伴被打时的通知范围）：大，群体才响应得起来。
	//
	// 两者都为 0 时不挂 AOI（该生物既不感知也不参与传播），
	// 此时**不会**有任何仇恨能力——这是显式选择，不是默认。
	Perception int
	Threat     int

	// HomeX/HomeY/RoamRadius 游荡锚点（0/0/0 = 不游荡）。
	HomeX, HomeY int
	RoamRadius   int
}

// SpawnAggroCapable 生成一只"能被打 → 记仇 → 通知同类"的生物。
//
// 挂载的组件正是契约里的最小发送方集合：
//
//	Position + Health + Attackable + Creature + AOI(Perception/Threat)
//
// 返回新实体。调用方若还需要别的能力（移动/攻击/行为树），自行 ecs.Add。
//
// 用法：
//
//	e := components.SpawnAggroCapable(w, components.AggroCapable{
//	    Kind: components.CreatureWolf, X: 10, Y: 10,
//	    HP: 30, Perception: 6, Threat: 16,
//	})
func SpawnAggroCapable(w *ecs.World, c AggroCapable) ecs.Entity {
	e := w.CreateEntity()
	AddAggroCapable(w, e, c)
	return e
}

// AddAggroCapable 给**已存在**的实体挂上仇恨所需的组件。
//
// 与 SpawnAggroCapable 分开，是为了支持"先建实体、后补能力"的场景
// （例如读档恢复、或给已生成的实体追加能力）。
func AddAggroCapable(w *ecs.World, e ecs.Entity, c AggroCapable) {
	hp := c.HP
	maxHP := c.MaxHP
	if maxHP <= 0 {
		maxHP = hp
	}

	ecs.Add(w, e, Position{X: c.X, Y: c.Y})
	ecs.Add(w, e, Health{Cur: hp, Max: maxHP})
	ecs.Add(w, e, Attackable{})
	ecs.Add(w, e, Creature{
		Kind:       c.Kind,
		Threats:    map[ecs.Entity]int32{},
		HomeX:      c.HomeX,
		HomeY:      c.HomeY,
		RoamRadius: c.RoamRadius,
	})

	// AOI 覆盖半径取两者较大者，**只做一份方格标记**（见 docs 第 4 节）。
	if c.Perception > 0 || c.Threat > 0 {
		radius := c.Perception
		if c.Threat > radius {
			radius = c.Threat
		}
		ecs.Add(w, e, AOI{
			Radius:     radius,
			Perception: c.Perception,
			Threat:     c.Threat,
		})
	}
}

// HasAggroCapability 报告实体是否具备**完整的**仇恨能力（发送方最小集）。
//
// 用于自检/调试：加新生物后可以断言它确实挂全了，
// 而不是等到"打了没反应"时才发现。
//
// 注意它检查的是"能不能传播出去"（发送方）；
// 接收方只需 Position + Health + Creature，比这个宽松。
func HasAggroCapability(w *ecs.World, e ecs.Entity) bool {
	return ecs.Has[Position](w, e) &&
		ecs.Has[Health](w, e) &&
		ecs.Has[Attackable](w, e) &&
		ecs.Has[Creature](w, e) &&
		ecs.Has[AOI](w, e)
}

// MissingAggroComponents 返回缺失的组件名（空 = 具备完整能力）。
//
// 给"加新生物后自检"用：比 bool 更利于定位到底漏了哪个。
func MissingAggroComponents(w *ecs.World, e ecs.Entity) []string {
	var missing []string
	if !ecs.Has[Position](w, e) {
		missing = append(missing, "Position")
	}
	if !ecs.Has[Health](w, e) {
		missing = append(missing, "Health")
	}
	if !ecs.Has[Attackable](w, e) {
		missing = append(missing, "Attackable")
	}
	if !ecs.Has[Creature](w, e) {
		missing = append(missing, "Creature")
	}
	if !ecs.Has[AOI](w, e) {
		missing = append(missing, "AOI")
	}
	return missing
}
