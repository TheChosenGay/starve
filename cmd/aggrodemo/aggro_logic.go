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
	ID     uint64  `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	HP     int     `json:"hp"`
	MaxHP  int     `json:"maxHp"`
	State  int32   `json:"state"`  // AI.State（idle/chase/attack/flee）
	Target uint64  `json:"target"` // 当前锁定的目标（0 = 无）
	// Direct 是**直接仇恨**对象（亲自打我 / 我看见的敌人）的实体 id。
	Direct uint64 `json:"direct"`
	// AttackedBy 记录**亲自打过它**的玩家 id（0 = 从没被攻击过）。
	//
	// 与 Direct 的区别：Direct 是"当前敌人"（可能只是**看见**了玩家，
	// 或已被更新的攻击者顶替）；AttackedBy 是"确实挨过打"的事实，不会被覆盖。
	// 前端据此画持久的红色标记——用户明确要求"被打的标红"。
	AttackedBy uint64 `json:"attackedBy"`
	// Indirect 是**间接仇恨**表：玩家实体 id → 我离他的距离（越近越优先）。
	// 多玩家场景下用它验证"仇恨归属是否正确"。
	Indirect map[uint64]int `json:"indirect"`
	AoiR     int            `json:"aoiR"`    // AOI 覆盖半径
	AoiPer   int            `json:"aoiPer"`  // 感知半径（发现敌人）
	AoiThr   int            `json:"aoiThr"`  // 仇恨传播半径
	Alive    bool           `json:"alive"`   //
	LastHit  int            `json:"lastHit"` // 最近一次分配到仇恨的 tick（闪烁提示）
}

// PlayerState 是玩家状态（多玩家时按索引区分）。
type PlayerState2 struct {
	ID   uint64  `json:"id"`
	Idx  int     `json:"idx"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	HP   int     `json:"hp"`
	MaxH int     `json:"maxHp"`
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
	Tick      int64          `json:"tick"`
	Players   []PlayerState2 `json:"players"`
	Wolves    []WolfState    `json:"wolves"`
	Events    []EventLine    `json:"events"`
	FieldHalf float64        `json:"fieldHalf"`
	Origin    float64        `json:"origin"`
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

	// 狼的两个半径，与 configs/creatures.json 对齐：
	//   perception 6 —— 感知范围（发现玩家）；小，保留潜行感
	//   threat    16 —— 仇恨传播范围（同伴被打时的通知范围）；大，狼群才响应得起来
	// AOI 的覆盖半径取两者较大者（只查一份，省一半方格标记成本）。
	demoWolfPerception = 6
	demoWolfThreat     = 16
	// demoAoiRadius 是 AOI 覆盖半径 = max(感知, 仇恨传播)。
	demoAoiRadius = 16

	demoPlayerHP    = 500
	demoPlayerSpeed = 10.0
	// demoPlayerCount 是默认玩家数（多个玩家 = 多个攻击源，
	// 用来验证"多攻击源时仇恨归属是否正确、会不会互相覆盖出错"）。
	demoPlayerCount = 2
	// demoPlayerDamage 是玩家单击一次的伤害。故意给大一点，
	// 让"打一下就能看出传播"（伤害越大，分摊到同伴的仇恨越多）。
	demoPlayerDamage = 12
)

// aggroWorld 是演示世界（真实 ECS）。
type aggroWorld struct {
	sim     *ecs.World
	players []ecs.Entity // 支持多个玩家同时攻击（验证多攻击源的仇恨归属）
	wolves  []ecs.Entity
	tick    int64
	dt      time.Duration

	events []EventLine
	// lastHitAt 记录每只狼最近一次"分配到仇恨"的 tick（含传播来的），
	// 前端据此闪烁，让"谁被通知了"一目了然。
	lastHitAt map[ecs.Entity]int64
	// attackedBy 记录"哪只狼被哪个玩家**亲自**打过"（事实，不随时间清除）。
	// 用于在画面上持久标红，区分"真的挨打了"与"只是收到了通知"。
	attackedBy map[ecs.Entity]ecs.Entity

	spreadRadius int
	spreadAmount int
}

// player 返回主玩家（前端"我"操控的那个）。
func (w *aggroWorld) player0() ecs.Entity {
	if len(w.players) == 0 {
		return 0
	}
	return w.players[0]
}

// newAggroWorld 建一个演示世界：N 个玩家 + M 只同类狼。
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
		sim:        sim,
		dt:         demoTick,
		lastHitAt:  map[ecs.Entity]int64{},
		attackedBy: map[ecs.Entity]ecs.Entity{},
	}
	w.spawnPlayers(demoPlayerCount)
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
	ecs.Add(w.sim, e, components.AOI{
		Radius:     demoAoiRadius,
		Perception: demoWolfPerception,
		Threat:     demoWolfThreat,
	})
	ecs.Add(w.sim, e, components.AI{
		State:          components.CreatureIdle,
		HitMemoryTicks: 10,
		FleeHP:         0, // 演示里不逃跑，保证能看到持续追击
		HostilePlayers: true,
		Leash:          demoWolfLeash,
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

// spawnPlayers 生成 n 个玩家，分散在场地边缘（便于观察"多个攻击源"）。
func (w *aggroWorld) spawnPlayers(n int) {
	w.players = w.players[:0]
	if n < 1 {
		n = 1
	}
	h := demoFieldHalf - 2
	// 固定几个方位，n 超过 4 时循环使用并稍微错开
	spots := [][2]float64{
		{demoOrigin, demoOrigin + h}, // 下方
		{demoOrigin - h, demoOrigin}, // 左方
		{demoOrigin + h, demoOrigin}, // 右方
		{demoOrigin, demoOrigin - h}, // 上方
	}
	for i := 0; i < n; i++ {
		sp := spots[i%len(spots)]
		off := float64(i/len(spots)) * 3
		w.players = append(w.players, w.spawnPlayer(sp[0]+off, sp[1]+off))
	}
}

// spawnPlayer 生成一个玩家（攻击者，用来触发群体仇恨）。
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

// attackWolf 让玩家 p 攻击指定的狼（前端点击某只狼时调用；p<0 = 主玩家）。
//
// 这是演示的入口：走的是**真实伤害路径**
// interactive/Attackable.ApplyDamage → Creature.AddThreat → SpreadThreatToAllies。
func (w *aggroWorld) attackWolf(idx int) bool {
	return w.attackWolfBy(idx, -1)
}

// attackWolfBy 指定攻击者（多玩家场景用来验证仇恨归属）。
func (w *aggroWorld) attackWolfBy(idx, playerIdx int) bool {
	if idx < 0 || idx >= len(w.wolves) {
		return false
	}
	attacker := w.player0()
	if playerIdx >= 0 && playerIdx < len(w.players) {
		attacker = w.players[playerIdx]
	}
	if attacker == 0 {
		return false
	}
	target := w.wolves[idx]
	if !w.sim.IsAlive(target) || ecs.Has[components.Dead](w.sim, target) {
		return false
	}
	before := w.threatSnapshot()
	components.Attackable{}.ApplyDamage(w.sim, target, attacker, demoPlayerDamage)
	w.lastHitAt[target] = w.tick
	w.attackedBy[target] = attacker // 记录"被谁亲自打过"（画红标记用）
	w.spreadAmount = demoPlayerDamage
	w.spreadRadius = demoWolfThreat

	// 记录"谁因为这次攻击获得了新仇恨"（传播的可见证据）。
	// 键是 (狼, 玩家)，所以这里只看本次攻击者那一列。
	after := w.threatSnapshot()
	gained := 0
	for _, wolf := range w.wolves {
		key := [2]ecs.Entity{wolf, attacker}
		if after[key] > before[key] {
			gained++
			if wolf != target {
				w.lastHitAt[wolf] = w.tick
			}
		}
	}
	w.logf("attack", "玩家#%d 攻击狼#%d（伤害 %d）→ %d 只同类获得仇恨",
		playerIdx, idx, demoPlayerDamage, gained-1)
	return true
}

// chaosAttack 让**多个玩家轮流/交叉攻击**不同的狼（压力场景）。
//
// 为什么需要它：单玩家场景只验证了"一条仇恨链"。多攻击源时会暴露
// 一些单源测不出来的问题——例如：
//   - 一只狼先后被两个玩家打，直接仇恨是否正确地从 A 切到 B（规则 ①）；
//   - A 的间接仇恨会不会错误地覆盖掉 B 的直接仇恨（规则 ②的"覆盖"边界）；
//   - 同一只狼既看见 A（直接）又收到 B 的传播（间接）时，选谁；
//   - 全场几百条仇恨同时刷新时会不会算错/卡顿。
//
// 分配方式用确定性伪随机（种子 = 轮次），保证同样的 rounds 得到同样的
// 场景，便于复现与回归对比。
func (w *aggroWorld) chaosAttack(rounds int) {
	if rounds < 1 {
		rounds = 1
	}
	if rounds > 50 {
		rounds = 50
	}
	if len(w.players) == 0 || len(w.wolves) == 0 {
		return
	}
	seed := uint64(w.tick)*2654435761 + uint64(rounds)
	hits := 0
	for r := 0; r < rounds; r++ {
		// 每轮：每个玩家各打一只狼（deterministic 选择）
		for pi := range w.players {
			seed = seed*6364136223846793005 + 1442695040888963407
			idx := int((seed >> 33) % uint64(len(w.wolves)))
			if w.attackWolfBy(idx, pi) {
				hits++
			}
		}
	}
	w.logf("chaos", "混战：%d 轮 × %d 玩家 = %d 次攻击", rounds, len(w.players), hits)
}

// threatSnapshot 取"每只狼 → 每个玩家"的仇恨值（用于对比传播前后）。
// 多玩家场景下必须按玩家分别统计，否则无法判断仇恨归属是否正确。
func (w *aggroWorld) threatSnapshot() map[[2]ecs.Entity]int32 {
	out := make(map[[2]ecs.Entity]int32)
	for _, e := range w.wolves {
		if !w.sim.IsAlive(e) {
			continue
		}
		c := ecs.Get[components.Creature](w.sim, e)
		for _, pl := range w.players {
			if v := c.ThreatOf(pl); v > 0 {
				out[[2]ecs.Entity{e, pl}] = v
			}
		}
	}
	return out
}

// movePlayer 把主玩家移动到指定坐标（前端拖动/点击）。
func (w *aggroWorld) movePlayer(x, y float64) { w.movePlayerBy(0, x, y) }

// movePlayerBy 移动指定玩家。
func (w *aggroWorld) movePlayerBy(idx int, x, y float64) {
	if idx < 0 || idx >= len(w.players) {
		return
	}
	e := w.players[idx]
	p := ecs.Get[components.Position](w.sim, e)
	p.X, p.Y = int(x), int(y)
	ecs.MarkDirty[components.Position](w.sim, e)
	mv := ecs.Get[components.Moveable](w.sim, e)
	mv.SubX, mv.SubY = 0, 0
	ecs.MarkDirty[components.Moveable](w.sim, e)
}

// setWolfCount 重建狼群（前端调整"狼群规模"滑块）。
func (w *aggroWorld) setWolfCount(n int) {
	for _, e := range w.wolves {
		w.sim.DestroyEntity(e)
	}
	w.wolves = nil
	w.lastHitAt = map[ecs.Entity]int64{}
	w.attackedBy = map[ecs.Entity]ecs.Entity{}
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

// reset 重开一局（狼群与玩家都重建）。
func (w *aggroWorld) reset(wolfCount, playerCount int) {
	for _, e := range w.wolves {
		w.sim.DestroyEntity(e)
	}
	for _, p := range w.players {
		w.sim.DestroyEntity(p)
	}
	w.wolves = nil
	w.players = nil
	w.lastHitAt = map[ecs.Entity]int64{}
	w.attackedBy = map[ecs.Entity]ecs.Entity{}
	w.events = nil
	w.tick = 0
	w.spawnPlayers(playerCount)
	w.spawnPack(wolfCount)
	w.step() // 重建后同样要预热 AOI.Visible（见 newAggroWorld 的说明）
	w.logf("notice", "重开：%d 只狼 · %d 个玩家", wolfCount, playerCount)
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
	// 玩家列表
	players := make([]PlayerState2, 0, len(w.players))
	for i, e := range w.players {
		ps := PlayerState2{ID: uint64(e), Idx: i}
		if w.sim.IsAlive(e) {
			pos := ecs.Get[components.Position](w.sim, e)
			hp := ecs.Get[components.Health](w.sim, e)
			ps.X, ps.Y, ps.HP, ps.MaxH = float64(pos.X), float64(pos.Y), hp.Cur, hp.Max
		}
		players = append(players, ps)
	}

	wolves := make([]WolfState, 0, len(w.wolves))
	aggro := 0
	for _, e := range w.wolves {
		st := WolfState{ID: uint64(e), Indirect: map[uint64]int{}}
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
		st.Direct = uint64(cr.DirectTarget())
		st.AttackedBy = uint64(w.attackedBy[e])
		// 间接仇恨：实体 id → 距离（用于前端显示"我离他几格"）
		for tgt, dist := range cr.Indirect {
			st.Indirect[uint64(tgt)] = dist
		}
		st.AoiR = aoi.Radius
		st.AoiPer = aoi.PerceptionRadius()
		st.AoiThr = aoi.ThreatRadius()
		st.Alive = true
		st.LastHit = int(w.lastHitAt[e])
		// 锁定的目标是否是某个玩家（用于"扑向谁"的统计）
		for _, pl := range w.players {
			if ai.Target == pl {
				aggro++
				break
			}
		}
		wolves = append(wolves, st)
	}

	events := make([]EventLine, len(w.events))
	copy(events, w.events)

	return Snapshot{
		Tick:         w.tick,
		Players:      players,
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
