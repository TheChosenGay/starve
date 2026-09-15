package main

import (
	"math"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/systems"
	"starve/internal/game/world/behavior"
)

// 本文件是 boss.html 的**纯逻辑层**：不引用 syscall/js，宿主机可单测。
//
// 它跑的是真实世界：真实 ecs.World、真实系统装配（systems.RegisterAll）、
// 真实行为树（TreeKindBoss）。浏览器只负责把每 tick 的快照画出来。
//
// 与正式服务器的差别只有两点（都是为了演示可控）：
//   - 不接 actor/网络，直接 RunSystems 推进；
//   - 世界不生成地图，用一块空场地，玩家位置由前端拖动（或自动追击 Boss）。

// Vec 是二维坐标（渲染用，对应游戏里的 X/Y 格坐标）。
type Vec struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Boss 状态（序列化给前端）。
type BossState struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	HP       int     `json:"hp"`
	MaxHP    int     `json:"maxHp"`
	Phase    int     `json:"phase"`    // 0=一阶段 2=二阶段
	State    int32   `json:"state"`    // AI.State（表现用）
	Target   uint64  `json:"target"`   // 当前目标
	Punching bool    `json:"punching"` // 是否在连拳中
}

// PlayerState 玩家状态。
type PlayerState struct {
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	HP int     `json:"hp"`
}

// BombState 是一枚飞行/待爆的炸弹。
type BombState struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Age  float64 `json:"age"`  // 已存在时长（秒）
	Fuse float64 `json:"fuse"` // 引信总时长（秒）
}

// BlastState 是一次爆炸的表现（扩散中的圈）。
type BlastState struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Radius float64 `json:"radius"`
	Age    float64 `json:"age"`
	Life   float64 `json:"life"`
}

// EventLine 是行为流水（给前端画"正在做什么"的日志与时间轴）。
type EventLine struct {
	Tick int64  `json:"tick"`
	Text string `json:"text"`
	Kind string `json:"kind"` // roar/leap/punch/slam/bomb/phase
}

// Snapshot 是每 tick 交给前端的完整状态。
type Snapshot struct {
	Tick    int64        `json:"tick"`
	Boss    BossState    `json:"boss"`
	Player  PlayerState  `json:"player"`
	Bombs   []BombState  `json:"bombs"`
	Blasts  []BlastState `json:"blasts"`
	Events  []EventLine  `json:"events"`
	LastAct string       `json:"lastAct"` // 本 tick 的行为（HUD 高亮）
}

// bossWorld 是演示世界（真实 ECS）。
type bossWorld struct {
	sim      *ecs.World
	boss     ecs.Entity
	player   ecs.Entity
	tick     int64
	dt       time.Duration
	bombs    []bombRuntime
	blasts   []blastRuntime
	events   []EventLine
	lastAct  string
	maxHP    int
	autoMove bool
}

// bombRuntime 是炸弹的运行时状态（落点 + 引信计时）。
type bombRuntime struct {
	x, y float64
	age  float64
	fuse float64
}

// blastRuntime 是爆炸表现的生命周期。
type blastRuntime struct {
	x, y   float64
	radius float64
	age    float64
	life   float64
}

// 演示参数
const (
	demoTick        = 50 * time.Millisecond // 20Hz，与正式服务器一致
	demoBossHP      = 400
	demoPhase2HP    = 200 // 掉到一半进二阶段
	demoBossSpeed   = 2.0 // 格/秒（比玩家慢，方便观察）
	demoPlayerSpeed = 3.0
	demoBombFuse    = 1.2 // 秒
	demoBombRadius  = 2.5 // 格
	demoBombDamage  = 6
	demoSlamRadius  = 3.0
	demoSlamDamage  = 14
	demoPunchDamage = 4
	// demoPunchCooldown 是普攻冷却（tick）。
	//
	// 注意出拳节奏由**两者共同**决定，取较长者：
	//   - 攻击动作本身的时间轴 windup+recovery = 8+8 = 16 tick（ActionExecutor）；
	//   - 这里的 AI.Cooldown。
	// 所以想让出拳更慢，必须把这个值设到 > 16，否则动作时间轴先结束、
	// 冷却形同虚设。取 24（1.2 秒）让打击感更清楚。
	demoPunchCooldown = 24 // tick
	demoMeleeRange    = 1
	// 场地：**必须用正坐标**（见 demoOrigin）。
	// AOI 感知网格按 y*Width+x 索引，负坐标会被 AOI 系统直接跳过
	// （aoi_system.go 的 `if x < 0 || y < 0 ... continue`），
	// 于是实体即使站在感知半径内也永远"看不见"——实测踩过：
	// 玩家走到 (14,-14) 后 Boss 立刻丢失目标、站着不动。
	demoFieldHalf = 16.0
	demoOrigin    = 32.0 // 场地中心的坐标（正数，留足 AOI 覆盖余量）

	// demoAoiRadius 是 Boss 的感知半径。
	//
	// 必须覆盖整个演示场地，否则玩家走远就"看不见"了：AI 的拴绳（leash）
	// 是 4 + AOI.Radius，而且用的是**曼哈顿**距离——对角 16 格 = 曼哈顿 32，
	// 走到角落就是 64。原先半径 30（leash=34）时，玩家一远离就会被判定
	// 超出拴绳、目标被清空，表现为 Boss 站着不动：既不投弹也不追击。
	demoAoiRadius = 64
)

// newBossWorld 建一个演示世界：一个 Boss + 一个玩家。
//
// 注意这里**手动装配**世界（而不是调 world.NewWorldActor）：后者需要地图、
// 配置文件和 actor 运行时，对演示过重。但**系统装配与行为树用的是真实实现**
// （systems.RegisterAll + components.TreeKindBoss），所以演示的行为就是
// 正式服务器里 Boss 的行为。
func newBossWorld() *bossWorld {
	sim := ecs.NewWorld()

	// 世界级资源（与 WorldActor 构造时一致，缺一不可）
	sim.AddResource(&components.DayCycle{})
	sim.AddResource(&components.DebugFlags{})
	sim.AddResource(&systems.AOIGrid{Width: demoGridSize, Height: demoGridSize})
	sim.AddResource(&systems.ControlQueue{})
	sim.AddResource(&systems.ActionCommitQueue{})
	sim.AddResource(systems.NewActionExecutorRegistry())
	sim.AddResource(&components.ActionMetrics{})
	sim.AddResource(&components.TickEventBuffer{})
	sim.AddResource(&components.BossActionQueue{})
	components.RegisterCodecs(sim, false)
	interactive.RegisterComponents(sim)
	// 注册交互行为（攻击/砍/挖/拾取）。
	//
	// **必须显式调用**：正式服务器里这件事由 internal/game/world 包的 init()
	// 完成（world/interact.go），而演示世界是直接建 ecs.World、不经过 world 包，
	// 所以不会自动执行。漏了它的后果很隐蔽——攻击意图在
	// AttackExecutor.Validate → interactive.CanDo 处被判为"目标非法"而拒绝，
	// 表现是"Boss 出拳没有冷却、也没有伤害"，但没有任何报错。
	behavior.Register()

	// 系统装配：与正式服务器**同一份**（顺序、实现都一致）
	systems.RegisterAll(sim, systems.Config{GrowthTicks: 20, AOIInterval: 1})

	w := &bossWorld{sim: sim, dt: demoTick, maxHP: demoBossHP}
	w.boss = w.spawnBoss(demoOrigin, demoOrigin)
	w.player = w.spawnPlayer(demoOrigin+8, demoOrigin)
	return w
}

// spawnBoss 生成 Boss：三阶段行为树 + 投弹/近战能力。
func (w *bossWorld) spawnBoss(x, y float64) ecs.Entity {
	e := w.sim.CreateEntity()
	ecs.Add(w.sim, e, components.Position{X: int(x), Y: int(y)})
	ecs.Add(w.sim, e, components.Health{Cur: demoBossHP, Max: demoBossHP})
	ecs.Add(w.sim, e, components.Attackable{})
	ecs.Add(w.sim, e, components.Moveable{Speed: demoBossSpeed, EffectiveSpeed: demoBossSpeed})
	ecs.Add(w.sim, e, components.AOI{Radius: demoAoiRadius})
	ecs.Add(w.sim, e, components.Creature{
		Kind: components.CreatureBoar, Threats: map[ecs.Entity]int32{},
		HomeX: int(x), HomeY: int(y), RoamRadius: 0,
	})
	ecs.Add(w.sim, e, components.AI{
		State:          components.CreatureIdle,
		HitMemoryTicks: 999, // Boss 不脱战，保证演示连续
		HostilePlayers: true,
		Phase2HP:       demoPhase2HP,
	})
	// 关键：挂 Boss 行为树
	ecs.Add(w.sim, e, components.BehaviorTree{
		Kind:         components.TreeKindBoss,
		RunningChild: map[uint32]uint8{},
		Counters:     map[uint32]int{},
	})
	ecs.Add(w.sim, e, interactive.Attacker{
		AttackDamage:   demoPunchDamage,
		AttackRange:    demoMeleeRange,
		AttackCooldown: demoPunchCooldown,
	})
	return e
}

// spawnPlayer 生成玩家（作为 Boss 的目标）。
func (w *bossWorld) spawnPlayer(x, y float64) ecs.Entity {
	e := w.sim.CreateEntity()
	ecs.Add(w.sim, e, components.Position{X: int(x), Y: int(y)})
	ecs.Add(w.sim, e, components.Health{Cur: 500, Max: 500})
	// Attackable 必须挂：攻击行为的前置校验（AttackBehavior.CanDo）要求
	// 目标带 Attackable，否则每次出拳都在 Validate 阶段被拒——
	// 表现是"拳头没有伤害、也没有冷却"（意图根本没被接纳，自然不会设冷却）。
	ecs.Add(w.sim, e, components.Attackable{})
	ecs.Add(w.sim, e, components.Player{})
	ecs.Add(w.sim, e, components.Moveable{Speed: demoPlayerSpeed, EffectiveSpeed: demoPlayerSpeed})
	return e
}

// step 推进一 tick，并消费行为树产出的 Boss 动作。
func (w *bossWorld) step() {
	w.lastAct = ""
	// ① 让 Boss 持续把玩家当敌人（演示不依赖 AOI 的降频感知）
	w.ensureThreat()

	// ② 推进全部真实系统（AI 会驱动行为树）
	w.sim.RunSystems(w.dt)
	w.sim.DrainEvents()

	// ③ 消费行为树产出的 Boss 动作意图
	for _, act := range components.DrainBossActions(w.sim) {
		w.applyBossAction(act)
	}

	// ④ 推进炸弹引信与爆炸表现
	w.stepBombs()
	w.stepBlasts()

	w.tick++
}

// ensureThreat 让玩家持续留在 Boss 的仇恨表里。
//
// 正式游戏里这由 AOI 感知 + 受击累加完成；演示里把 AOI 间隔设为 1 已能
// 正常感知，这里再兜一层：玩家死亡/走远也不脱战，便于连续观察行为。
func (w *bossWorld) ensureThreat() {
	if !w.sim.IsAlive(w.boss) || !w.sim.IsAlive(w.player) {
		return
	}
	c := ecs.Get[components.Creature](w.sim, w.boss)
	if c.Threats == nil {
		c.Threats = map[ecs.Entity]int32{}
	}
	if c.Threats[w.player] < 50 {
		c.Threats[w.player] = 50
	}
}

// applyBossAction 把行为树的意图变成演示世界里的实际效果。
func (w *bossWorld) applyBossAction(act components.BossAction) {
	switch act.Kind {
	case components.BossActionThrowBomb:
		w.spawnBombAt(act.Target)
		w.log("bomb", "投掷炸弹")
	case components.BossActionLeap:
		w.log("leap", "闪现突进")
	case components.BossActionRoar:
		w.log("roar", "嚎叫！进入第二阶段")
	case components.BossActionSlam:
		w.doSlam(act.Actor, float64(act.Radius))
		w.log("slam", "锤地 AOE")
	case components.BossActionPunch:
		// 出拳只记流水、不抢"当前动作"高亮（拳很快，高亮留给大招更有信息量）
		w.logQuiet("punch", "出拳")
	}
}

// spawnBombAt 在目标当前位置生成一枚炸弹（落点 = 玩家当前位置）。
func (w *bossWorld) spawnBombAt(target ecs.Entity) {
	if target == 0 || !ecs.Has[components.Position](w.sim, target) {
		return
	}
	p := ecs.Get[components.Position](w.sim, target)
	w.bombs = append(w.bombs, bombRuntime{
		x: float64(p.X), y: float64(p.Y), fuse: demoBombFuse,
	})
}

// doSlam 结算锤地 AOE：以 Boss 为中心、半径内的实体受到伤害。
//
// 这里用**距离判定**而不是碰撞索引：演示场地是空的，没有障碍物，
// 距离判定与"AOE 球体查询"等价且更直观。
func (w *bossWorld) doSlam(actor ecs.Entity, radius float64) {
	if !ecs.Has[components.Position](w.sim, actor) {
		return
	}
	center := ecs.Get[components.Position](w.sim, actor)
	w.blasts = append(w.blasts, blastRuntime{
		x: float64(center.X), y: float64(center.Y),
		radius: radius, life: 0.45,
	})
	// 对玩家造成伤害（真实扣血走 Health 组件）
	if w.sim.IsAlive(w.player) && ecs.Has[components.Position](w.sim, w.player) {
		pp := ecs.Get[components.Position](w.sim, w.player)
		d := math.Hypot(float64(pp.X-center.X), float64(pp.Y-center.Y))
		if d <= radius {
			hp := ecs.Get[components.Health](w.sim, w.player)
			hp.Cur -= demoSlamDamage
			if hp.Cur < 0 {
				hp.Cur = 0
			}
			ecs.MarkDirty[components.Health](w.sim, w.player)
		}
	}
}

// stepBombs 推进炸弹：引信到点则爆炸（对玩家做半径判定）。
//
// 注意**不能**用 `alive := w.bombs[:0]` 这种"原地过滤"写法：
// 同一 tick 里 applyBossAction → spawnBombAt 还会 append 到 w.bombs，
// 而 `w.bombs[:0]` 复用的是同一块底层数组——两个切片互相踩内存，
// 结果是炸弹被反复"复活"、永不消失（实测同时存在 21+ 颗，
// 日志被"炸弹命中玩家"刷屏，二阶段看起来仍在投弹）。
// 这里新建切片，彻底切断别名。
func (w *bossWorld) stepBombs() {
	dt := w.dt.Seconds()
	alive := make([]bombRuntime, 0, len(w.bombs))
	for _, b := range w.bombs {
		b.age += dt
		if b.age >= b.fuse {
			w.explode(b.x, b.y, demoBombRadius, demoBombDamage)
			continue
		}
		alive = append(alive, b)
	}
	w.bombs = alive
}

// explode 结算一次爆炸（对玩家做半径判定 + 记录表现）。
func (w *bossWorld) explode(x, y, radius float64, damage int) {
	w.blasts = append(w.blasts, blastRuntime{x: x, y: y, radius: radius, life: 0.4})
	if !w.sim.IsAlive(w.player) || !ecs.Has[components.Position](w.sim, w.player) {
		return
	}
	pp := ecs.Get[components.Position](w.sim, w.player)
	if math.Hypot(float64(pp.X)-x, float64(pp.Y)-y) > radius {
		return
	}
	hp := ecs.Get[components.Health](w.sim, w.player)
	hp.Cur -= damage
	if hp.Cur < 0 {
		hp.Cur = 0
	}
	ecs.MarkDirty[components.Health](w.sim, w.player)
	w.logQuiet("bomb", "炸弹命中玩家")
}

// stepBlasts 推进爆炸表现的生命周期。
func (w *bossWorld) stepBlasts() {
	dt := w.dt.Seconds()
	alive := w.blasts[:0]
	for _, b := range w.blasts {
		b.age += dt
		if b.age < b.life {
			alive = append(alive, b)
		}
	}
	w.blasts = alive
}

// log 记一条行为流水（前端做时间轴/日志）。
//
// 注意它**会**更新 lastAct。炸弹爆炸这类"非 Boss 决策"的事件应当用
// logQuiet，否则会把 Boss 本 tick 的决策（例如"嚎叫"）覆盖掉——
// 实测踩过：嚎叫那一 tick 恰好有炸弹引爆，"当前动作"就显示成了投掷炸弹，
// 看起来像"二阶段还在投弹"。
func (w *bossWorld) log(kind, text string) {
	w.lastAct = kind
	w.appendEvent(kind, text)
}

// logQuiet 只记流水、不改变"当前动作"（用于炸弹命中/爆炸等表现类事件）。
func (w *bossWorld) logQuiet(kind, text string) {
	w.appendEvent(kind, text)
}

func (w *bossWorld) appendEvent(kind, text string) {
	w.events = append(w.events, EventLine{Tick: w.tick, Text: text, Kind: kind})
	if len(w.events) > 60 {
		w.events = w.events[len(w.events)-60:]
	}
}

// snapshot 取当前状态（交给前端渲染）。
func (w *bossWorld) snapshot() Snapshot {
	bp := ecs.Get[components.Position](w.sim, w.boss)
	bh := ecs.Get[components.Health](w.sim, w.boss)
	bai := ecs.Get[components.AI](w.sim, w.boss)
	pp := ecs.Get[components.Position](w.sim, w.player)
	ph := ecs.Get[components.Health](w.sim, w.player)

	// 注意：切片必须**初始化为空切片**（而不是 nil）。
	// Go 的 encoding/json 把 nil 切片编码成 `null`，而前端是 `for (const x of ...)`
	// 直接遍历——遇到 null 会抛 "is not iterable" 把渲染循环整个打断。
	snap := Snapshot{
		Tick:   w.tick,
		Bombs:  []BombState{},
		Blasts: []BlastState{},
		Events: []EventLine{},
		Boss: BossState{
			X: float64(bp.X), Y: float64(bp.Y),
			HP: bh.Cur, MaxHP: bh.Max, Phase: bai.Phase,
			State: int32(bai.State), Target: uint64(bai.Target),
			Punching: ecs.Has[components.ActionState](w.sim, w.boss),
		},
		Player:  PlayerState{X: float64(pp.X), Y: float64(pp.Y), HP: ph.Cur},
		LastAct: w.lastAct,
	}
	for _, b := range w.bombs {
		snap.Bombs = append(snap.Bombs, BombState{X: b.x, Y: b.y, Age: b.age, Fuse: b.fuse})
	}
	for _, b := range w.blasts {
		snap.Blasts = append(snap.Blasts, BlastState{
			X: b.x, Y: b.y, Radius: b.radius, Age: b.age, Life: b.life,
		})
	}
	// 注意：必须用 `make(...)` 而不是 `append([]EventLine(nil), ...)`。
	// 后者在源为空时返回 **nil**，encoding/json 会编码成 `null`，
	// 前端 `for...of` 直接抛 "is not iterable"（页面白屏）。
	// 上面 Snapshot 字面量里的 `Events: []EventLine{}` 就是被这行覆盖掉的。
	snap.Events = make([]EventLine, len(w.events))
	copy(snap.Events, w.events)
	return snap
}

// damageBoss 给 Boss 造成伤害（前端"打 Boss"按钮，用于推进到二阶段）。
func (w *bossWorld) damageBoss(amount int) {
	if !w.sim.IsAlive(w.boss) {
		return
	}
	hp := ecs.Get[components.Health](w.sim, w.boss)
	hp.Cur -= amount
	if hp.Cur < 0 {
		hp.Cur = 0
	}
	ecs.MarkDirty[components.Health](w.sim, w.boss)
}

// movePlayer 把玩家移动到指定格（前端拖动）。
func (w *bossWorld) movePlayer(x, y float64) {
	if !w.sim.IsAlive(w.player) {
		return
	}
	p := ecs.Get[components.Position](w.sim, w.player)
	p.X = clampCoord(x)
	p.Y = clampCoord(y)
	ecs.MarkDirty[components.Position](w.sim, w.player)
}

// demoGridSize 是感知网格边长：覆盖 demoOrigin ± demoFieldHalf 还有余量。
const demoGridSize = 96

// clampCoord 把坐标夹取到场地的**正坐标**范围内。
//
// 场地范围是 [demoOrigin-demoFieldHalf, demoOrigin+demoFieldHalf]。
// 之所以不能是负数：AOI 网格按 y*Width+x 索引，负坐标会被跳过（见 demoOrigin 注释）。
func clampCoord(v float64) int {
	lo := demoOrigin - demoFieldHalf
	hi := demoOrigin + demoFieldHalf
	r := math.Round(v)
	if r < lo {
		r = lo
	}
	if r > hi {
		r = hi
	}
	return int(r)
}

// reset 重开一局。
func (w *bossWorld) reset() {
	*w = *newBossWorld()
}

// TreeDescription 是行为树结构的文本描述（前端侧栏展示"决策树长什么样"）。
type TreeDescription struct {
	Kind  string `json:"kind"`
	Nodes int    `json:"nodes"`
	Text  string `json:"text"`
}

// treeDescription 返回当前 Boss 所用行为树的结构（走真实定义 + Describe）。
func (w *bossWorld) treeDescription() TreeDescription {
	if !w.sim.IsAlive(w.boss) {
		return TreeDescription{}
	}
	kind := ecs.Get[components.BehaviorTree](w.sim, w.boss).Kind
	tree := components.TreeOf(kind)
	if tree == nil {
		return TreeDescription{}
	}
	return TreeDescription{
		Kind:  kind.String(),
		Nodes: tree.NodeCount(),
		Text:  tree.Describe(),
	}
}
