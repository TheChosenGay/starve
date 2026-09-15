package systems

import (
	"starve/internal/ecs"
	"starve/internal/game/behavior"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/worldmap"
)

// 本文件是行为树与 ECS 的**桥接层**：把实体上的组件映射成 behavior 包的
// Blackboard / Env 接口。
//
// 为什么放在 systems 而不是 components：桥接需要读 interactive.Attacker
// （玩家手中的武器），而 components 包不能 import interactive（会成环：
// interactive 已经 import components）。systems 已经依赖两者，是天然的
// 汇合点——这与 AISystem 里 weaponOf 的做法一致。

// btBoard 是 behavior.Blackboard 的 ECS 实现。
//
// 它是每 tick 临时构造的轻量视图（只持有 world + 实体），字段按需实时读
// 组件，不做数据拷贝——避免"黑板与组件不同步"这类隐蔽 bug。
type btBoard struct {
	w *ecs.World
	e ecs.Entity
}

// newBoard 把实体包装成行为树黑板。
func newBoard(w *ecs.World, e ecs.Entity) behavior.Blackboard {
	return &btBoard{w: w, e: e}
}

func (b *btBoard) Target() uint64 {
	if !ecs.Has[components.AI](b.w, b.e) {
		return 0
	}
	return uint64(ecs.Get[components.AI](b.w, b.e).Target)
}

func (b *btBoard) SetTarget(e uint64) {
	if ecs.Has[components.AI](b.w, b.e) {
		target := ecs.Entity(e)
		ai := ecs.Get[components.AI](b.w, b.e)
		if ai.Target != target {
			ai.Target = target
			ecs.MarkDirty[components.AI](b.w, b.e)
		}
	}
}

func (b *btBoard) Self() uint64 { return uint64(b.e) }

func (b *btBoard) Health() int {
	if !ecs.Has[components.Health](b.w, b.e) {
		return 0
	}
	return ecs.Get[components.Health](b.w, b.e).Cur
}

func (b *btBoard) HealthMax() int {
	if !ecs.Has[components.Health](b.w, b.e) {
		return 0
	}
	return ecs.Get[components.Health](b.w, b.e).Max
}

func (b *btBoard) FleeHP() int {
	if !ecs.Has[components.AI](b.w, b.e) {
		return 0
	}
	return ecs.Get[components.AI](b.w, b.e).FleeHP
}

func (b *btBoard) AttackDamage() int        { return b.weapon().AttackDamage }
func (b *btBoard) AttackRange() int         { return b.weapon().AttackRange }
func (b *btBoard) AttackCooldownTicks() int { return b.weapon().AttackCooldown }

// weapon 取攻击能力：优先装备（interactive.Attacker），其次实体自身的
// Weapon 组件（生物生成时从模板挂的那个）。
func (b *btBoard) weapon() components.Weapon {
	if ecs.Has[interactive.Attacker](b.w, b.e) {
		a := ecs.Get[interactive.Attacker](b.w, b.e)
		return components.Weapon{
			AttackRange:    a.AttackRange,
			AttackDamage:   a.AttackDamage,
			AttackCooldown: a.AttackCooldown,
		}
	}
	if ecs.Has[components.Weapon](b.w, b.e) {
		return *ecs.Get[components.Weapon](b.w, b.e)
	}
	return components.Weapon{}
}

func (b *btBoard) InAttackRange() bool {
	if !ecs.Has[components.AI](b.w, b.e) || !ecs.Has[components.Position](b.w, b.e) {
		return false
	}
	t := ecs.Get[components.AI](b.w, b.e).Target
	if t == 0 || !ecs.Has[components.Position](b.w, t) {
		return false
	}
	cp := ecs.Get[components.Position](b.w, b.e)
	return cp.WithinRange(*ecs.Get[components.Position](b.w, t), b.weapon().AttackRange)
}

func (b *btBoard) CanSee(e uint64) bool {
	if !ecs.Has[components.AOI](b.w, b.e) {
		return false
	}
	target := ecs.Entity(e)
	for _, v := range ecs.Get[components.AOI](b.w, b.e).Visible {
		if v == target {
			return true
		}
	}
	return false
}

func (b *btBoard) RoamRadius() int {
	if !ecs.Has[components.Creature](b.w, b.e) {
		return 0
	}
	return ecs.Get[components.Creature](b.w, b.e).RoamRadius
}

func (b *btBoard) HomeX() int {
	if !ecs.Has[components.Creature](b.w, b.e) {
		return 0
	}
	return ecs.Get[components.Creature](b.w, b.e).HomeX
}

func (b *btBoard) HomeY() int {
	if !ecs.Has[components.Creature](b.w, b.e) {
		return 0
	}
	return ecs.Get[components.Creature](b.w, b.e).HomeY
}

func (b *btBoard) Now() int { return worldPhase(b.w) }

// --- 多阶段（Boss）黑板 ---

func (b *btBoard) Phase() int {
	if !ecs.Has[components.AI](b.w, b.e) {
		return 0
	}
	return ecs.Get[components.AI](b.w, b.e).Phase
}

func (b *btBoard) SetPhase(p int) {
	if !ecs.Has[components.AI](b.w, b.e) {
		return
	}
	ai := ecs.Get[components.AI](b.w, b.e)
	if ai.Phase == p {
		return
	}
	ai.Phase = p
	// 切阶段时清掉行为树的运行态：阶段是**决策语境**的变化，
	// 上一阶段"正在做的动作"不该被带进新阶段继续（例如阶段一的投弹
	// 游标不该让阶段二继续投弹）。嚎叫的 Once 标记也随之重置——
	// 但因为只在切阶段那一刻清一次，之后不会再清，所以仍然只嚎叫一次。
	if ecs.Has[components.BehaviorTree](b.w, b.e) {
		bt := ecs.Get[components.BehaviorTree](b.w, b.e)
		bt.RunningChild = map[uint32]uint8{}
		bt.Counters = map[uint32]int{}
		ecs.MarkDirty[components.BehaviorTree](b.w, b.e)
	}
	ecs.MarkDirty[components.AI](b.w, b.e)
}

func (b *btBoard) Phase2HP() int {
	if !ecs.Has[components.AI](b.w, b.e) {
		return 0
	}
	return ecs.Get[components.AI](b.w, b.e).Phase2HP
}

// Busy 当前是否有权威动作在进行。
func (b *btBoard) Busy() bool {
	return ecs.Has[components.ActionState](b.w, b.e)
}

// DistanceToTarget 到目标的曼哈顿距离（无目标返回 -1）。
func (b *btBoard) DistanceToTarget() int {
	if !ecs.Has[components.AI](b.w, b.e) || !ecs.Has[components.Position](b.w, b.e) {
		return -1
	}
	t := ecs.Get[components.AI](b.w, b.e).Target
	if t == 0 || !ecs.Has[components.Position](b.w, t) {
		return -1
	}
	cp := ecs.Get[components.Position](b.w, b.e)
	return cp.Manhattan(*ecs.Get[components.Position](b.w, t))
}

var _ behavior.Blackboard = (*btBoard)(nil)

// btEnv 是 behavior.Env 的 ECS 实现：把动作节点翻译成**控制意图**。
//
// 关键约定（与原 AISystem 完全一致）：动作节点不直接改位置/结算伤害，
// 只 EnqueueControl 提交意图，由 ControlSystem 统一仲裁。这样行为树保持
// "纯决策层"，多人/多 AI 抢同一控制时的仲裁逻辑不用重复实现。
type btEnv struct {
	w *ecs.World
	e ecs.Entity
}

func newEnv(w *ecs.World, e ecs.Entity) behavior.Env {
	return &btEnv{w: w, e: e}
}

// Rand 确定性随机：种子 = 世界相位 ^ 实体 id ^ 树内节点无关常量，
// 与 AISystem 原先的 splitmix 约定一致（同种子同结果，可重放）。
func (e *btEnv) Rand(n int) int {
	if n <= 0 {
		return 0
	}
	seed := uint64(worldPhase(e.w)) ^ uint64(e.e)*0x9E3779B97F4A7C15
	return int(splitmix(seed) % uint64(n))
}

func (e *btEnv) MoveDir(dx, dy int) {
	if dx == 0 && dy == 0 {
		return
	}
	EnqueueControl(e.w, MoveIntent(e.e, dx, dy, 0))
}

func (e *btEnv) MovePath(path []behavior.MoveStep) {
	if len(path) == 0 {
		return
	}
	steps := make([]components.MoveDir, 0, len(path))
	for _, s := range path {
		steps = append(steps, components.MoveDir{DX: s.DX, DY: s.DY})
	}
	EnqueueControl(e.w, PathIntent(e.e, steps, 0))
}

// MoveToward 朝目标移动一步：有地图走 A*（最多 16 步前瞻），否则退化为贪心方向。
// 与 AISystem.chase 的行为保持一致（含"有地图但不可达则不动"的处理）。
func (e *btEnv) MoveToward(target uint64) {
	t := ecs.Entity(target)
	if t == 0 || !ecs.Has[components.Position](e.w, t) || !ecs.Has[components.Position](e.w, e.e) {
		return
	}
	if !ecs.Has[components.Moveable](e.w, e.e) {
		return
	}
	mv := ecs.Get[components.Moveable](e.w, e.e)
	if len(mv.Path) > 0 {
		return // 路径未走完，MoveSystem 连续跟随
	}
	cp := ecs.Get[components.Position](e.w, e.e)
	tp := ecs.Get[components.Position](e.w, t)
	if md, ok := ecs.TryResource[worldmap.MapData](e.w); ok {
		if path := worldmap.FindPath(md, cp.X, cp.Y, tp.X, tp.Y); len(path) > 0 {
			if len(path) > 16 {
				path = path[:16]
			}
			e.MovePath(stepsOf(path))
		}
		return // 有地图但不可达：不贪心下水
	}
	e.MoveDir(signOf(tp.X-cp.X), signOf(tp.Y-cp.Y))
}

// FleeFrom 远离目标：优先用地图的 FleeDir（挑可走方向），否则反向直走。
func (e *btEnv) FleeFrom(target uint64) {
	t := ecs.Entity(target)
	if t == 0 || !ecs.Has[components.Position](e.w, t) || !ecs.Has[components.Position](e.w, e.e) {
		return
	}
	cp := ecs.Get[components.Position](e.w, e.e)
	tp := ecs.Get[components.Position](e.w, t)
	dx, dy := 0, 0
	if md, ok := ecs.TryResource[worldmap.MapData](e.w); ok {
		dx, dy = worldmap.FleeDir(md, cp.X, cp.Y, tp.X, tp.Y)
	} else {
		dx, dy = signOf(cp.X-tp.X), signOf(cp.Y-tp.Y)
	}
	e.MoveDir(dx, dy)
}

// MoveHome 朝出生点回防一步（超出游荡半径时用）。
func (e *btEnv) MoveHome() {
	if !ecs.Has[components.Creature](e.w, e.e) || !ecs.Has[components.Position](e.w, e.e) {
		return
	}
	c := ecs.Get[components.Creature](e.w, e.e)
	cp := ecs.Get[components.Position](e.w, e.e)
	e.MoveDir(signOf(c.HomeX-cp.X), signOf(c.HomeY-cp.Y))
}

// StartAttack 发起攻击动作（伤害结算在 ActionSystem 的 commit 阶段）。
func (e *btEnv) StartAttack(target uint64) {
	if ecs.Has[components.ActionState](e.w, e.e) {
		return // 已有权威动作在进行，不抢占
	}
	EnqueueControl(e.w, StartActionIntent(e.e, components.ActionAttack, ecs.Entity(target), 0, 0))
}

// AttackReady 攻击冷却是否结束。
//
// 优先看 AI.Cooldown（原状态机用的字段，由 ActionSystem/攻击流程维护）；
// 行为树的 Cooldown 装饰器负责**发起**节流，两者配合：
// 装饰器管"多久能再试一次"，AI.Cooldown 管"动作本身还没打完"。
func (e *btEnv) AttackReady() bool {
	if !ecs.Has[components.AI](e.w, e.e) {
		return true
	}
	return ecs.Get[components.AI](e.w, e.e).Cooldown <= 0
}

// HomeDistance 距出生点的曼哈顿距离。
func (e *btEnv) HomeDistance() int {
	if !ecs.Has[components.Creature](e.w, e.e) || !ecs.Has[components.Position](e.w, e.e) {
		return 0
	}
	c := ecs.Get[components.Creature](e.w, e.e)
	cp := ecs.Get[components.Position](e.w, e.e)
	return cp.Manhattan(components.Position{X: c.HomeX, Y: c.HomeY})
}

// --- Boss 能力 ---
//
// 这些能力目前以"事件/意图"的形式挂在世界资源上（见 boss_events.go），
// 由 systems 或演示层消费。这样行为树保持纯决策层，不直接改位置/血量，
// 与项目"系统产出意图、统一仲裁"的既有分工一致。

// ThrowBomb 朝目标投一枚炸弹（记录一次投弹意图）。
func (e *btEnv) ThrowBomb(target uint64) {
	components.EmitBossAction(e.w, components.BossActionThrowBomb, e.e, ecs.Entity(target), 0)
}

// LeapTo 瞬间位移到目标身边。
//
// 这里是**真位移**（闪现是原子操作，没有"飞行中"的中间态），
// 但仍然走统一的落点校验：落在目标相邻的可走格，避免卡进障碍。
func (e *btEnv) LeapTo(target uint64) bool {
	t := ecs.Entity(target)
	if t == 0 || !ecs.Has[components.Position](e.w, t) || !ecs.Has[components.Position](e.w, e.e) {
		return false
	}
	tp := ecs.Get[components.Position](e.w, t)
	from := *ecs.Get[components.Position](e.w, e.e)
	landing, ok := leapLanding(e.w, *tp, from)
	if !ok {
		return false
	}
	cp := ecs.Get[components.Position](e.w, e.e)
	cp.X, cp.Y = landing.X, landing.Y
	if mv := ecs.Get[components.Moveable](e.w, e.e); mv != nil {
		// 清掉子格偏移与残留路径：闪现是瞬移，不该保留上一段的位移惯性。
		mv.SubX, mv.SubY = 0, 0
		mv.Path = nil
		ecs.MarkDirty[components.Moveable](e.w, e.e)
	}
	ecs.MarkDirty[components.Position](e.w, e.e)
	components.EmitBossAction(e.w, components.BossActionLeap, e.e, t, 0)
	return true
}

// leapLanding 选一个贴住目标的落点：目标相邻 8 格里第一个可走的。
//
// 为什么不在目标正下方：那会和目标重叠（两个实体占同一格），
// 后续近战判定与碰撞都会变得别扭。相邻格既"贴脸"又不重叠。
func leapLanding(w *ecs.World, target, from components.Position) (components.Position, bool) {
	offsets := [8][2]int{
		{1, 0}, {-1, 0}, {0, 1}, {0, -1},
		{1, 1}, {1, -1}, {-1, 1}, {-1, -1},
	}
	md, hasMap := ecs.TryResource[worldmap.MapData](w)
	best := components.Position{}
	bestDist := -1
	for _, off := range offsets {
		p := components.Position{X: target.X + off[0], Y: target.Y + off[1]}
		if hasMap && !md.Walkable(p.X, p.Y) {
			continue
		}
		// 选离"来的方向"最近的落点：视觉上像从原位置冲过来，而不是绕到背后。
		d := p.Manhattan(from)
		if bestDist < 0 || d < bestDist {
			best, bestDist = p, d
		}
	}
	if bestDist < 0 {
		return components.Position{}, false
	}
	return best, true
}

// SlamAOE 释放一次范围攻击。
func (e *btEnv) SlamAOE() {
	components.EmitBossAction(e.w, components.BossActionSlam, e.e, 0, bossSlamRadius)
}

// Roar 嚎叫一次。
func (e *btEnv) Roar() {
	components.EmitBossAction(e.w, components.BossActionRoar, e.e, 0, 0)
}

// Punch 对目标打一拳（复用攻击动作时间轴，保证伤害在 commit 阶段结算）。
func (e *btEnv) Punch(target uint64) {
	e.StartAttack(target)
}

// ActionBusy 当前是否有权威动作在进行。
func (e *btEnv) ActionBusy() bool {
	return ecs.Has[components.ActionState](e.w, e.e)
}

// bossSlamRadius 是锤地 AOE 的半径（格）。
const bossSlamRadius = 3

// stepsOf 把 worldmap 的路径点转成 behavior.MoveStep。
func stepsOf(path []components.MoveDir) []behavior.MoveStep {
	out := make([]behavior.MoveStep, 0, len(path))
	for _, p := range path {
		out = append(out, behavior.MoveStep{DX: p.DX, DY: p.DY})
	}
	return out
}

var _ behavior.Env = (*btEnv)(nil)
