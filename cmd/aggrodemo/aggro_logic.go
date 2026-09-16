package main

import (
	"fmt"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/systems"
	"starve/internal/game/world/behavior"
)

// 本文件是 aggro.html 的**纯逻辑层**：不引用 syscall/js，宿主机可单测。
//
// 它跑的是真实世界：真实 ecs.World、真实系统装配（systems.RegisterAll）、
// 真实行为树、以及**真实的群体仇恨**（Creature.AddThreat → SpreadThreatToAllies）。
//
// 演示目标：让"仇恨传播"这件事**看得见**。
//   - 一队同类狼（默认 6 只）排开站在场地里；
//   - 玩家点击/拖动靠近某一只并攻击它；
//   - 被攻击的那只获得完整仇恨并按行为树反击；
//   - 感知范围内的**同类**收到按距离衰减的仇恨，也一起扑上来；
//   - 每只狼头顶显示自己的仇恨值，传播范围画成方框（AOI 是正方形感知）。

// Vec 是二维坐标（渲染用，对应游戏里的 X/Y 格坐标）。
type Vec struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// WolfState 是一只狼的对外状态（前端渲染 + 仇恨可视化）。
type WolfState struct {
	ID       uint64  `json:"id"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	HP       int     `json:"hp"`
	MaxHP    int     `json:"maxHp"`
	State    int32   `json:"state"`    // AI.State（idle/chase/attack/flee）
	Target   uint64  `json:"target"`   // 当前锁定的目标（0 = 无）
	Threat   int32   `json:"threat"`   // 对玩家的仇恨值（演示的核心观测量）
	Direct   bool    `json:"direct"`   // 是否"亲自"被玩家打过（优先级更高）
	AoiR     int     `json:"aoiR"`     // 感知半径
	Alive    bool    `json:"alive"`    //
	LastHit  int     `json:"lastHit"`  // 最近一次分配到仇恨的 tick（用于闪烁提示）
	DistToPl float64 `json:"distToPl"` // 到玩家的切比雪夫距离（与分摊用的距离一致）
}

// PlayerState 是玩家状态。
type PlayerState struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	HP   int     `json:"hp"`
	MaxH int     `json:"maxHp"`
}

// EventLine 是仇恨流水（给前端画日志时间轴）。
type EventLine struct {
	Tick int64  `json:"tick"`
	Text string `json:"text"`
	Kind string `json:"kind"` // attack/spread/direct/kill/notice
}

// Snapshot 是每 tick 交给前端的完整状态。
type Snapshot struct {
	Tick      int64       `json:"tick"`
	Player    PlayerState `json:"player"`
	Wolves    []WolfState `json:"wolves"`
	Events    []EventLine `json:"events"`
	FieldHalf float64     `json:"fieldHalf"`
	Origin    float64     `json:"origin"`
	// SpreadRadius / SpreadAmount 是本次攻击的传播参数（前端画范围提示用）。
	SpreadRadius int `json:"spreadRadius"`
	SpreadAmount int `json:"spreadAmount"`
	// AggroCount 是"当前有多少只同类把玩家当目标"（一行看懂的指标）。
	AggroCount  int `json:"aggroCount"`
	TotalWolves int `json:"totalWolves"`
}

// 演示参数。
const (
	demoTick = 50 * time.Millisecond // 20Hz，与正式服务器一致

	// 场地：**必须用正坐标**。AOI 感知网格按 y*Width+x 索引，
	// 负坐标会被 aoi_system 直接跳过，实体即使站在半径内也永远"看不见"。
	demoOrigin    = 48.0
	demoFieldHalf = 20.0
	demoGridSize  = 128

	// 狼群参数：与 configs/creatures.json 的 wolf 保持一致，
	// 这样演示里的手感就是正式服务器的真实手感。
	demoWolfHP       = 30
	demoWolfDamage   = 8
	demoWolfCooldown = 30
	demoWolfRange    = 1
	demoWolfSpeed    = 6.7
	demoWolfLeash    = 30

	// demoAoiRadius 是狼的感知半径（= creatures.json 的 perception_radius）。
	// 群体仇恨的传播范围就是它：只有在这个正方形内的同类才会被"通知"。
	demoAoiRadius = 6

	// demoThreatDecayTicks 仇恨衰减间隔（tick），与 creatures.json 的 wolf 一致。
	demoThreatDecayTicks = 10

	demoPlayerHP    = 500
	demoPlayerSpeed = 10.0
	// demoPlayerDamage 是玩家单击一次的伤害。故意给大一点，
	// 让"打一下就能看出传播"（伤害越大，分摊到同伴的仇恨越多）。
	demoPlayerDamage = 12
)

// aggroWorld 是演示世界（真实 ECS）。
type aggroWorld struct {
	sim    *ecs.World
	player ecs.Entity
	wolves []ecs.Entity
	tick   int64
	dt     time.Duration

	events []EventLine
	// lastHitAt 记录每只狼最近一次"分配到仇恨"的 tick（含传播来的），
	// 前端据此闪烁，让"谁被通知了"一目了然。
	lastHitAt map[ecs.Entity]int64

	spreadRadius int
	spreadAmount int
}

// newAggroWorld 建一个演示世界：1 个玩家 + N 只同类狼。
//
// 注意这里**手动装配**世界（不调 world.NewWorldActor）：后者需要地图与配置，
// 对演示过重。但**系统装配与行为树用的是真实实现**（systems.RegisterAll、
// TreeKindPredator），仇恨传播也是真实的 Creature.AddThreat 路径，
// 所以演示里的群体反应就是正式服务器里的行为。
func newAggroWorld(wolfCount int) *aggroWorld {
	sim := ecs.NewWorld()

	// 世界级资源（缺一不可，与 WorldActor 构造时一致）
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
	// **必须显式调用**：正式服务器里这件事由 internal/game/world 包的 init()
	// 完成，而演示世界直接建 ecs.World、不经过 world 包。
	// 漏了它的后果很隐蔽——攻击意图在 Validate 阶段被拒，表现为
	// "能选中目标但打不出伤害、也没有冷却"，且没有任何报错。
	behavior.Register()

	// 系统装配：与正式服务器**同一份**
	systems.RegisterAll(sim, systems.Config{GrowthTicks: 20, AOIInterval: 1})

	w := &aggroWorld{
		sim:       sim,
		dt:        demoTick,
		lastHitAt: map[ecs.Entity]int64{},
	}
	w.player = w.spawnPlayer(demoOrigin, demoOrigin+demoFieldHalf-2)
	w.spawnPack(wolfCount)
	// 先跑一 tick，让 AOISystem 填好每只狼的 Visible。
	//
	// 为什么必须预热：AOI.Visible 是**派生缓存**，由 AOISystem 每 tick 重算，
	// 刚建出来的实体它是空的。而群体仇恨正是靠 Visible 找邻居——
	// 若不预热，页面刚打开时点第一下"打狼"会**完全没有传播**
	// （表现是"打了半天只有那一只有反应"），看起来像机制没生效，实则是时序问题。
	w.step()
	return w
}

// spawnPack 生成一队同类狼：排成一行，间距 2 格。
//
// 间距 2 是刻意的：狼的感知半径是 6，所以整队狼都互相"看得见"，
// 打任意一只都会传播给其余所有狼——这就是"群体仇恨"最直观的样子。
func (w *aggroWorld) spawnPack(n int) {
	if n < 1 {
		n = 1
	}
	w.wolves = w.wolves[:0]
	// 以场地中心为基准横向排开
	startX := demoOrigin - float64(n-1)*2/2
	for i := 0; i < n; i++ {
		x := startX + float64(i)*2
		y := demoOrigin
		w.wolves = append(w.wolves, w.spawnWolf(x, y))
	}
}

// spawnWolf 生成一只狼（掠食者行为树 + 攻击能力 + 感知）。
func (w *aggroWorld) spawnWolf(x, y float64) ecs.Entity {
	e := w.sim.CreateEntity()
	ecs.Add(w.sim, e, components.Position{X: int(x), Y: int(y)})
	ecs.Add(w.sim, e, components.Health{Cur: demoWolfHP, Max: demoWolfHP})
	ecs.Add(w.sim, e, components.Attackable{})
	ecs.Add(w.sim, e, components.Moveable{Speed: demoWolfSpeed, EffectiveSpeed: demoWolfSpeed})
	ecs.Add(w.sim, e, components.Creature{
		Kind:       components.CreatureWolf,
		Threats:    map[ecs.Entity]int32{},
		HomeX:      int(x),
		HomeY:      int(y),
		RoamRadius: 0, // 0 = 不游荡，站着等演示
	})
	// 感知半径 = 群体仇恨的传播范围
	ecs.Add(w.sim, e, components.AOI{Radius: demoAoiRadius})
	ecs.Add(w.sim, e, components.AI{
		State:          components.CreatureIdle,
		HitMemoryTicks: 10,
		// 仇恨衰减间隔，与 configs/creatures.json 的 wolf 对齐。
		// 缺省 10 tick（0.5 秒）衰减 1 点，让"被通知的同伴"能维持数秒仇恨、
		// 真的从远处跑过来加入战斗（每 tick -1 时只够撑 200~400ms）。
		ThreatDecayTicks: demoThreatDecayTicks,
		FleeHP:           0, // 演示里不逃跑，保证能看到持续追击
		HostilePlayers:   true,
		Leash:            demoWolfLeash,
	})
	// 攻击能力：必须挂 Weapon，否则 AttackDamage()==0，
	// projectedState 会把"有目标"当成被动生物的逃跑（那是 prey 的语义）。
	ecs.Add(w.sim, e, components.Weapon{
		AttackRange:    demoWolfRange,
		AttackDamage:   demoWolfDamage,
		AttackCooldown: demoWolfCooldown,
	})
	ecs.Add(w.sim, e, components.BehaviorTree{
		Kind:         components.TreeKindPredator,
		RunningChild: map[uint32]uint8{},
		Counters:     map[uint32]int{},
	})
	return e
}

// spawnPlayer 生成玩家（也是唯一的"攻击者"，用来触发群体仇恨）。
func (w *aggroWorld) spawnPlayer(x, y float64) ecs.Entity {
	e := w.sim.CreateEntity()
	ecs.Add(w.sim, e, components.Position{X: int(x), Y: int(y)})
	ecs.Add(w.sim, e, components.Health{Cur: demoPlayerHP, Max: demoPlayerHP})
	// Attackable 必须挂：攻击行为的前置校验要求目标带 Attackable，
	// 否则每次攻击都在 Validate 阶段被拒（没有伤害也没有冷却）。
	ecs.Add(w.sim, e, components.Attackable{})
	ecs.Add(w.sim, e, components.Player{})
	ecs.Add(w.sim, e, components.Moveable{Speed: demoPlayerSpeed, EffectiveSpeed: demoPlayerSpeed})
	return e
}

// attackWolf 让玩家攻击指定的狼（前端点击某只狼时调用）。
//
// 这是演示的入口：走的是**真实伤害路径**
// interactive/Attackable.ApplyDamage → Creature.AddThreat → SpreadThreatToAllies。
func (w *aggroWorld) attackWolf(idx int) bool {
	if idx < 0 || idx >= len(w.wolves) {
		return false
	}
	target := w.wolves[idx]
	if !w.sim.IsAlive(target) || ecs.Has[components.Dead](w.sim, target) {
		return false
	}
	before := w.threatSnapshot()
	components.Attackable{}.ApplyDamage(w.sim, target, w.player, demoPlayerDamage)
	w.lastHitAt[target] = w.tick
	w.spreadAmount = demoPlayerDamage
	w.spreadRadius = demoAoiRadius

	// 记录"谁因为这次攻击获得了新仇恨"（传播的可见证据）
	after := w.threatSnapshot()
	gained := 0
	for e, v := range after {
		if v > before[e] {
			gained++
			if e != target {
				w.lastHitAt[e] = w.tick
			}
		}
	}
	w.logf("attack", "攻击狼#%d（伤害 %d）→ %d 只同类获得仇恨", idx, demoPlayerDamage, gained-1)
	return true
}

// threatSnapshot 取当前所有狼对玩家的仇恨值（用于对比传播前后）。
func (w *aggroWorld) threatSnapshot() map[ecs.Entity]int32 {
	out := make(map[ecs.Entity]int32, len(w.wolves))
	for _, e := range w.wolves {
		if !w.sim.IsAlive(e) {
			continue
		}
		out[e] = ecs.Get[components.Creature](w.sim, e).ThreatOf(w.player)
	}
	return out
}

// movePlayer 把玩家移动到指定坐标（前端拖动/点击）。
func (w *aggroWorld) movePlayer(x, y float64) {
	p := ecs.Get[components.Position](w.sim, w.player)
	p.X, p.Y = int(x), int(y)
	ecs.MarkDirty[components.Position](w.sim, w.player)
	mv := ecs.Get[components.Moveable](w.sim, w.player)
	mv.SubX, mv.SubY = 0, 0
	ecs.MarkDirty[components.Moveable](w.sim, w.player)
}

// setWolfCount 重建狼群（前端调整"狼群规模"滑块）。
func (w *aggroWorld) setWolfCount(n int) {
	for _, e := range w.wolves {
		w.sim.DestroyEntity(e)
	}
	w.wolves = nil
	w.lastHitAt = map[ecs.Entity]int64{}
	w.spawnPack(n)
	w.step() // 同上：预热 AOI.Visible
	w.logf("notice", "重建狼群：%d 只", n)
}

// step 推进一 tick（真实系统装配）。
func (w *aggroWorld) step() {
	w.tick++
	w.sim.RunSystems(w.dt)
	w.sim.DrainEvents()
}

// reset 重开一局。
func (w *aggroWorld) reset(wolfCount int) {
	for _, e := range w.wolves {
		w.sim.DestroyEntity(e)
	}
	w.wolves = nil
	w.lastHitAt = map[ecs.Entity]int64{}
	w.events = nil
	w.tick = 0
	// 玩家回到出发点并回满血
	hp := ecs.Get[components.Health](w.sim, w.player)
	hp.Cur = demoPlayerHP
	w.movePlayer(demoOrigin, demoOrigin+demoFieldHalf-2)
	w.spawnPack(wolfCount)
	w.step() // 重建后同样要预热 AOI.Visible（见 newAggroWorld 的说明）
	w.logf("notice", "重开：%d 只狼", wolfCount)
}

func (w *aggroWorld) logf(kind, format string, args ...any) {
	w.events = append(w.events, EventLine{
		Tick: w.tick,
		Text: fmt.Sprintf(format, args...),
		Kind: kind,
	})
	// 环形缓冲：只留最近 60 条（与 bossdemo 一致，避免快照无限增长）
	const limit = 60
	if len(w.events) > limit {
		w.events = w.events[len(w.events)-limit:]
	}
}

// snapshot 生成前端快照。
//
// 注意所有切片都必须**非 nil**（用 make 而不是 var）：nil 切片会被
// encoding/json 序列化成 `null`，前端 `for...of` 直接抛
// "is not iterable" 并白屏——bossdemo 踩过这个坑。
func (w *aggroWorld) snapshot() Snapshot {
	wolves := make([]WolfState, 0, len(w.wolves))
	aggro := 0
	plPos := ecs.Get[components.Position](w.sim, w.player)
	plHP := ecs.Get[components.Health](w.sim, w.player)

	for _, e := range w.wolves {
		st := WolfState{ID: uint64(e)}
		if !w.sim.IsAlive(e) || ecs.Has[components.Dead](w.sim, e) {
			st.Alive = false
			st.LastHit = int(w.lastHitAt[e])
			wolves = append(wolves, st)
			continue
		}
		pos := ecs.Get[components.Position](w.sim, e)
		hp := ecs.Get[components.Health](w.sim, e)
		ai := ecs.Get[components.AI](w.sim, e)
		cr := ecs.Get[components.Creature](w.sim, e)
		aoi := ecs.Get[components.AOI](w.sim, e)

		st.X, st.Y = float64(pos.X), float64(pos.Y)
		st.HP, st.MaxHP = hp.Cur, hp.Max
		st.State = int32(ai.State)
		st.Target = uint64(ai.Target)
		st.Threat = cr.ThreatOf(w.player)
		st.Direct = cr.IsDirectThreat(w.player)
		st.AoiR = aoi.Radius
		st.Alive = true
		st.LastHit = int(w.lastHitAt[e])
		st.DistToPl = chebyshev(pos.X, pos.Y, plPos.X, plPos.Y)
		if ai.Target == w.player {
			aggro++
		}
		wolves = append(wolves, st)
	}

	events := make([]EventLine, len(w.events))
	copy(events, w.events)

	return Snapshot{
		Tick:         w.tick,
		Player:       PlayerState{X: float64(plPos.X), Y: float64(plPos.Y), HP: plHP.Cur, MaxH: plHP.Max},
		Wolves:       wolves,
		Events:       events,
		FieldHalf:    demoFieldHalf,
		Origin:       demoOrigin,
		SpreadRadius: w.spreadRadius,
		SpreadAmount: w.spreadAmount,
		AggroCount:   aggro,
		TotalWolves:  len(w.wolves),
	}
}

// chebyshev 切比雪夫距离：与仇恨分摊用的距离口径一致（AOI 是正方形感知）。
func chebyshev(ax, ay, bx, by int) float64 {
	dx := ax - bx
	if dx < 0 {
		dx = -dx
	}
	dy := ay - by
	if dy < 0 {
		dy = -dy
	}
	if dy > dx {
		return float64(dy)
	}
	return float64(dx)
}
