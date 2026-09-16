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

// 本文件是 throw.html 的**纯逻辑层**：不引用 syscall/js，宿主机可单测。
//
// 它跑的是真实世界：真实 ecs.World、真实系统装配（systems.RegisterAll）、
// 真实的 ThrowBehavior 校验与 ThrowSystem 飞行/落地结算。
//
// 演示目标：把"投掷"这件事的每个环节都**看得见**：
//   - 瞄准：点地图选落点，实时显示"能否投掷 / 为什么不能"
//   - 力量与质量：距离上限 = 基础距离 × 力量 / 质量（可调，立刻见效）
//   - 抛物线：服务端算出的弧线参数（飞行时长 + 峰值高度）画成轨迹
//   - 落地：爆炸范围 + 被炸生物**记仇并传播**（与群体仇恨联动）

// Vec 是二维坐标（渲染用）。
type Vec struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// PropState 是一件可投掷物的状态。
type PropState struct {
	ID     uint64  `json:"id"`
	Name   string  `json:"name"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Mass   int     `json:"mass"`
	Flying bool    `json:"flying"` // 是否在飞行中
	// 飞行参数（flying 时有效）：客户端据此画抛物线
	FromX       float64 `json:"fromX"`
	FromY       float64 `json:"fromY"`
	ToX         float64 `json:"toX"`
	ToY         float64 `json:"toY"`
	FlightTicks int     `json:"flightTicks"`
	Elapsed     int     `json:"elapsed"`
	PeakHeight  float64 `json:"peakHeight"`
	Gravity     float64 `json:"gravity"`
}

// BeastState 是一只野生动物（爆炸的受害者，用来展示仇恨联动）。
type BeastState struct {
	ID       uint64  `json:"id"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	HP       int     `json:"hp"`
	MaxHP    int     `json:"maxHp"`
	Alive    bool    `json:"alive"`
	Threat   bool    `json:"threat"`   // 是否已对投掷者记仇（直接仇恨）
	State    int32   `json:"state"`    // AI.State
	Flashing int     `json:"flashing"` // 最近被炸到的 tick
}

// PlayerState 是投掷者。
type PlayerState struct {
	ID       uint64  `json:"id"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Strength int     `json:"strength"`
	MaxDist  int     `json:"maxDist"` // 对当前选中投掷物的最大距离
}

// BlastState 是一次爆炸的表现。
type BlastState struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Radius float64 `json:"radius"`
	Age    float64 `json:"age"`
	Life   float64 `json:"life"`
}

// EventLine 是事件流水。
type EventLine struct {
	Tick int64  `json:"tick"`
	Text string `json:"text"`
	Kind string `json:"kind"` // throw/land/blocked/notice
}

// Snapshot 是每 tick 交给前端的完整状态。
type Snapshot struct {
	Tick      int64        `json:"tick"`
	Player    PlayerState  `json:"player"`
	Props     []PropState  `json:"props"`
	Beasts    []BeastState `json:"beasts"`
	Blasts    []BlastState `json:"blasts"`
	Events    []EventLine  `json:"events"`
	FieldHalf float64      `json:"fieldHalf"`
	Origin    float64      `json:"origin"`
	// Selected 是当前选中的投掷物下标（前端高亮）。
	Selected int `json:"selected"`
	// Aim 是当前瞄准点（HasAim=false 时无意义）。
	AimX   float64 `json:"aimX"`
	AimY   float64 `json:"aimY"`
	HasAim bool    `json:"hasAim"`
	// CanThrow/BlockReason 是**实时校验结果**（服务端权威），
	// 让用户看到"为什么不能扔"而不是静默失败。
	CanThrow    bool   `json:"canThrow"`
	BlockReason string `json:"blockReason"`
	// BlastCount 是累计爆炸次数（只增不减，用于"曾经爆炸过"的判断与展示）。
	BlastCount int `json:"blastCount"`
	// PreviewArc 是瞄准点的预览抛物线（未投掷）。
	PreviewArc *ArcState `json:"previewArc"`
}

// ArcState 是抛物线参数（预览与实际共用）。
type ArcState struct {
	FromX       float64 `json:"fromX"`
	FromY       float64 `json:"fromY"`
	ToX         float64 `json:"toX"`
	ToY         float64 `json:"toY"`
	FlightTicks int     `json:"flightTicks"`
	PeakHeight  float64 `json:"peakHeight"`
	Gravity     float64 `json:"gravity"`
}

// 演示参数。
const (
	demoTick      = 50 * time.Millisecond // 20Hz
	demoOrigin    = 48.0
	demoFieldHalf = 24.0
	demoGridSize  = 160

	demoPlayerHP = 200
	// 投掷者的力量：与 creatures.json 无关，演示里固定。
	// 距离上限 = BaseThrowDistance(8) × Strength / Mass。
	demoStrength = 20
)

// throwWorld 是演示世界（真实 ECS）。
type throwWorld struct {
	sim    *ecs.World
	player ecs.Entity
	props  []ecs.Entity
	beasts []ecs.Entity
	tick   int64
	dt     time.Duration

	selected int
	aimX     float64
	aimY     float64
	hasAim   bool

	blasts    []blastRuntime
	events    []EventLine
	lastBlast map[ecs.Entity]int64 // 生物最近被炸的 tick（闪烁）
	lastHP    map[ecs.Entity]int   // 上一 tick 的 HP（用于判断"刚被炸到"）
	// blastCount 是累计爆炸次数（只增不减）。
	//
	// 为什么需要它：`blasts` 是**表现**（life 0.5 秒后会过期移除），
	// 用它判断"是否爆炸过"会因时序而漏判（实测踩过：落地后跑满 30 tick，
	// 表现已过期，断言 len(blasts)==0 误判为"没爆炸"）。
	// 累计计数表达的是**事实**，不受表现生命周期影响。
	blastCount int
}

type blastRuntime struct {
	x, y, radius float64
	age, life    float64
}

// newThrowWorld 建一个演示世界：1 个投掷者 + N 件投掷物 + M 只野兽。
//
// 手动装配世界（不调 world.NewWorldActor），但**系统装配与投掷逻辑用的是
// 真实实现**（systems.RegisterAll + behavior.ThrowBehavior + ThrowSystem）。
func newThrowWorld(propCount, beastCount int) *throwWorld {
	sim := ecs.NewWorld()

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

	systems.RegisterAll(sim, systems.Config{GrowthTicks: 20, AOIInterval: 1})

	w := &throwWorld{
		sim:       sim,
		dt:        demoTick,
		lastBlast: map[ecs.Entity]int64{},
		lastHP:    map[ecs.Entity]int{},
	}
	w.player = w.spawnPlayer(demoOrigin-demoFieldHalf+4, demoOrigin)
	w.spawnProps(propCount)
	w.spawnBeasts(beastCount)
	w.selected = 0
	w.step() // 预热 AOI.Visible（见 aggrodemo 的同款说明）
	return w
}

// spawnPlayer 生成投掷者：带 Thrower（力量）。
func (w *throwWorld) spawnPlayer(x, y float64) ecs.Entity {
	e := w.sim.CreateEntity()
	ecs.Add(w.sim, e, components.Position{X: int(x), Y: int(y)})
	ecs.Add(w.sim, e, components.Health{Cur: demoPlayerHP, Max: demoPlayerHP})
	ecs.Add(w.sim, e, components.Player{})
	// 投掷能力挂在**玩家自身**（真实实现里也可以来自手持装备，
	// ActorCap 会优先取手部装备，没有则用自身——两者都支持）。
	ecs.Add(w.sim, e, interactive.Thrower{Strength: demoStrength})
	return e
}

// spawnProps 生成若干可投掷物，质量各不相同（用来展示"越重扔越近"）。
func (w *throwWorld) spawnProps(n int) {
	w.props = w.props[:0]
	if n < 1 {
		n = 1
	}
	// 质量档位：石头轻、木头中、巨石重
	specs := []struct {
		name string
		mass int
	}{
		{"石头", 4}, {"圆木", 10}, {"巨石", 25}, {"铁块", 40},
	}
	// 玩家坐标（投掷物必须落在"手里"范围 ThrowHandRange=2 内，否则校验会拒）
	var px, py float64
	if ecs.Has[components.Position](w.sim, w.player) {
		pp := ecs.Get[components.Position](w.sim, w.player)
		px, py = float64(pp.X), float64(pp.Y)
	}
	for i := 0; i < n; i++ {
		sp := specs[i%len(specs)]
		e := w.sim.CreateEntity()
		// 摆在玩家身边一圈（切比雪夫距离 <= 2 = "在手里"）。
		//
		// 为什么必须贴近：投掷校验要求被投物在投掷者身边（防"隔空扔别人脚下的
		// 东西"）。演示若把它们摆远，所有投掷都会被拒——实测踩过：
		// 全部显示"被投掷物不在手里"，看起来像机制坏了，实则是布局问题。
		// 用一圈偏移，8 件也能排下且都在 2 格内。
		offsets := [][2]float64{
			{1, 0}, {2, 0}, {1, 1}, {2, 1},
			{1, -1}, {2, -1}, {1, 2}, {2, 2},
		}
		off := offsets[i%len(offsets)]
		x, y := px+off[0], py+off[1]
		ecs.Add(w.sim, e, components.Position{X: int(x), Y: int(y)})
		ecs.Add(w.sim, e, components.Throwable{Mass: sp.mass})
		// 爆炸属性：演示里的投掷物**全部可炸**（否则落地什么都不发生，
		// 看不出效果）。爆炸参数从组件读，与正式服务器的炸弹一致。
		ecs.Add(w.sim, e, components.Explosive{
			Radius:    components.DefaultBlastRadius,
			Damage:    components.DefaultBlastDamage,
			Knockback: components.DefaultBlastKnockback,
		})
		ecs.Add(w.sim, e, components.Block{Width: 1, Height: 1, Thin: true})
		w.props = append(w.props, e)
	}
}

// spawnBeasts 生成野生动物（爆炸受害者），用来展示"被炸会记仇"。
func (w *throwWorld) spawnBeasts(n int) {
	w.beasts = w.beasts[:0]
	if n < 1 {
		n = 1
	}
	for i := 0; i < n; i++ {
		x := demoOrigin + 4 + float64(i%5)*3
		y := demoOrigin - 8 + float64(i/5)*3
		e := w.sim.CreateEntity()
		ecs.Add(w.sim, e, components.Position{X: int(x), Y: int(y)})
		ecs.Add(w.sim, e, components.Health{Cur: 30, Max: 30})
		ecs.Add(w.sim, e, components.Attackable{})
		ecs.Add(w.sim, e, components.Creature{
			Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{},
		})
		ecs.Add(w.sim, e, components.AOI{Radius: 16, Perception: 6, Threat: 16})
		ecs.Add(w.sim, e, components.AI{HostilePlayers: true, Leash: 30})
		// 行为树是**唯一**决策来源（AI.State 只是它的投影）。
		// 不挂树的话野兽会一直 idle：仇恨记上了但不会有任何行为，
		// 看起来像"记仇没生效"。演示里用掠食者树。
		ecs.Add(w.sim, e, components.BehaviorTree{
			Kind:         components.TreeKindPredator,
			RunningChild: map[uint32]uint8{},
			Counters:     map[uint32]int{},
		})
		// 攻击能力：不挂 Weapon 时 AttackDamage()==0，投影会变成 flee（被动语义）。
		ecs.Add(w.sim, e, components.Weapon{AttackRange: 1, AttackDamage: 8, AttackCooldown: 30})
		w.beasts = append(w.beasts, e)
	}
}

// aimAt 设置瞄准点（前端点击地图）。返回校验结果。
func (w *throwWorld) aimAt(x, y float64) {
	w.aimX, w.aimY, w.hasAim = x, y, true
}

// selectProp 切换当前选中的投掷物。
func (w *throwWorld) selectProp(i int) {
	if i >= 0 && i < len(w.props) {
		w.selected = i
	}
}

// setStrength 调整投掷者力量（前端滑块），立刻影响可达距离。
func (w *throwWorld) setStrength(v int) {
	if v < 0 {
		v = 0
	}
	if _, t := interactive.ActorCap[interactive.Thrower](w.sim, w.player); t != nil {
		t.Strength = v
	}
}

// currentProp 返回当前选中的投掷物（0 = 无）。
func (w *throwWorld) currentProp() ecs.Entity {
	if w.selected < 0 || w.selected >= len(w.props) {
		return 0
	}
	return w.props[w.selected]
}

// previewArc 返回瞄准点的预览抛物线（不产生副作用）。
//
// 用的是**真实的 CanThrow 校验**——所以前端显示的"能否投掷 / 为什么不能"
// 与服务端实际执行时的判断完全一致（同一份代码），不会出现"看起来能扔、
// 实际被拒"的脱节。
func (w *throwWorld) previewArc() (behavior.ThrowRequest, components.ThrowArc, string) {
	if !w.hasAim {
		return behavior.ThrowRequest{}, components.ThrowArc{}, "未瞄准"
	}
	prop := w.currentProp()
	if prop == 0 || !ecs.Has[components.Position](w.sim, prop) {
		return behavior.ThrowRequest{}, components.ThrowArc{}, "没有可投掷物"
	}
	p := ecs.Get[components.Position](w.sim, prop)
	req := behavior.ThrowRequest{
		Thrown: prop,
		FromX:  float64(p.X),
		FromY:  float64(p.Y),
		ToX:    w.aimX,
		ToY:    w.aimY,
	}
	arc, reason := behavior.ThrowBehavior{}.CanThrow(w.sim, w.player, req)
	return req, arc, reason
}

// doThrow 执行投掷（前端按钮）。返回是否成功。
func (w *throwWorld) doThrow() bool {
	req, _, reason := w.previewArc()
	if reason != "" {
		w.logf("blocked", "投掷被拒：%s", reason)
		return false
	}
	res := behavior.ThrowBehavior{}.Throw(w.sim, w.player, req)
	if !res.Success {
		w.logf("blocked", "投掷被拒：%s", res.Reason)
		return false
	}
	w.logf("throw", "投出：飞行 %d tick（%.1f 秒），峰值高度 %.1f 格",
		res.Arc.FlightTicks, float64(res.Arc.FlightTicks)*0.05, res.Arc.PeakHeight)
	return true
}

// step 推进一步（真实系统装配：ThrowSystem 会推进飞行与落地结算）。
func (w *throwWorld) step() {
	w.tick++
	w.sim.RunSystems(w.dt)
	w.sim.DrainEvents()
	// 消费爆炸事件（服务端权威结果 → 前端表现）。
	//
	// 用 DrainTickEvents（它取走并清空缓冲），不要手动切 Events：
	// 领域事件走 TickEventBuffer（WorldEvent），与 ecs.DrainEvents
	// （ECS 内部副作用通道）是两回事。演示世界直接跑 RunSystems、
	// 不经过 WorldActor，所以这里必须自己消费，否则事件会一直累积。
	for _, ev := range components.DrainTickEvents(w.sim) {
		if b := ev.GetBlast(); b != nil {
			w.blasts = append(w.blasts, blastRuntime{
				x: float64(b.X), y: float64(b.Y), radius: float64(b.Radius), life: 0.5,
			})
			w.blastCount++
			w.logf("land", "爆炸：中心 (%.0f,%.0f) 半径 %.1f", b.X, b.Y, b.Radius)
		}
	}
	w.lastBlastTick()
	w.stepBlasts()
}

// lastBlastTick 记录"哪些生物刚被炸到"（按 HP 下降推断，用于前端闪烁）。
//
// 这里刻意不去读内部结算细节：演示只关心"它掉血了"，掉血就闪。
func (w *throwWorld) lastBlastTick() {
	for _, e := range w.beasts {
		if !w.sim.IsAlive(e) || !ecs.Has[components.Health](w.sim, e) {
			continue
		}
		hp := ecs.Get[components.Health](w.sim, e)
		if last, ok := w.lastHP[e]; ok && hp.Cur < last {
			w.lastBlast[e] = w.tick
		}
		w.lastHP[e] = hp.Cur
	}
}

func (w *throwWorld) stepBlasts() {
	const dt = 0.05
	alive := make([]blastRuntime, 0, len(w.blasts))
	for _, b := range w.blasts {
		b.age += dt
		if b.age < b.life {
			alive = append(alive, b)
		}
	}
	w.blasts = alive
}

func (w *throwWorld) logf(kind, format string, args ...any) {
	w.events = append(w.events, EventLine{
		Tick: w.tick,
		Text: fmt.Sprintf(format, args...),
		Kind: kind,
	})
	const limit = 60
	if len(w.events) > limit {
		w.events = w.events[len(w.events)-limit:]
	}
}

// reset 重开一局。
func (w *throwWorld) reset(propCount, beastCount int) {
	for _, e := range append(append([]ecs.Entity{}, w.props...), w.beasts...) {
		w.sim.DestroyEntity(e)
	}
	w.props = nil
	w.beasts = nil
	w.blasts = nil
	w.events = nil
	w.lastBlast = map[ecs.Entity]int64{}
	w.lastHP = map[ecs.Entity]int{}
	w.blastCount = 0
	w.tick = 0
	w.selected = 0
	w.hasAim = false
	w.spawnProps(propCount)
	w.spawnBeasts(beastCount)
	w.step()
	w.logf("notice", "重开：%d 件投掷物 · %d 只野兽", propCount, beastCount)
}

// snapshot 生成前端快照。
//
// 所有切片必须**非 nil**（nil 会被 JSON 成 null，前端 for...of 直接白屏）。
func (w *throwWorld) snapshot() Snapshot {
	plPos := components.Position{}
	if ecs.Has[components.Position](w.sim, w.player) {
		plPos = *ecs.Get[components.Position](w.sim, w.player)
	}
	strength := 0
	if _, t := interactive.ActorCap[interactive.Thrower](w.sim, w.player); t != nil {
		strength = t.Strength
	}

	// 选中物的最大投掷距离
	maxDist := 0
	if prop := w.currentProp(); prop != 0 && ecs.Has[components.Throwable](w.sim, prop) {
		maxDist = components.MaxThrowDistance(strength, ecs.Get[components.Throwable](w.sim, prop).Mass)
	}

	props := make([]PropState, 0, len(w.props))
	for i, e := range w.props {
		ps := PropState{ID: uint64(e), Name: propName(i)}
		if !w.sim.IsAlive(e) {
			props = append(props, ps)
			continue
		}
		if ecs.Has[components.Position](w.sim, e) {
			p := ecs.Get[components.Position](w.sim, e)
			ps.X, ps.Y = float64(p.X), float64(p.Y)
		}
		if ecs.Has[components.Throwable](w.sim, e) {
			ps.Mass = ecs.Get[components.Throwable](w.sim, e).Mass
		}
		if ecs.Has[components.Thrown](w.sim, e) {
			th := ecs.Get[components.Thrown](w.sim, e)
			ps.Flying = true
			ps.FromX, ps.FromY = th.FromX, th.FromY
			ps.ToX, ps.ToY = th.ToX, th.ToY
			ps.FlightTicks, ps.Elapsed = th.FlightTicks, th.Elapsed
			ps.Gravity = th.Gravity
			ps.PeakHeight = components.PeakHeight(th.FlightTicks, th.Gravity)
		}
		props = append(props, ps)
	}

	beasts := make([]BeastState, 0, len(w.beasts))
	for _, e := range w.beasts {
		bs := BeastState{ID: uint64(e)}
		if !w.sim.IsAlive(e) || ecs.Has[components.Dead](w.sim, e) {
			beasts = append(beasts, bs)
			continue
		}
		if ecs.Has[components.Position](w.sim, e) {
			p := ecs.Get[components.Position](w.sim, e)
			bs.X, bs.Y = float64(p.X), float64(p.Y)
		}
		if ecs.Has[components.Health](w.sim, e) {
			hp := ecs.Get[components.Health](w.sim, e)
			bs.HP, bs.MaxHP = hp.Cur, hp.Max
		}
		bs.Alive = true
		if ecs.Has[components.Creature](w.sim, e) {
			// 是否已对投掷者记仇（直接仇恨）——展示"被炸会记仇"
			bs.Threat = ecs.Get[components.Creature](w.sim, e).IsDirectThreat(w.player)
		}
		if ecs.Has[components.AI](w.sim, e) {
			bs.State = int32(ecs.Get[components.AI](w.sim, e).State)
		}
		if t, ok := w.lastBlast[e]; ok && w.tick-t < 10 {
			bs.Flashing = int(w.tick - t)
		}
		beasts = append(beasts, bs)
	}

	blasts := make([]BlastState, 0, len(w.blasts))
	for _, b := range w.blasts {
		blasts = append(blasts, BlastState{X: b.x, Y: b.y, Radius: b.radius, Age: b.age, Life: b.life})
	}
	events := make([]EventLine, len(w.events))
	copy(events, w.events)

	_, arc, reason := w.previewArc()
	var preview *ArcState
	if reason == "" {
		preview = &ArcState{
			FromX: arc.FromX, FromY: arc.FromY,
			ToX: arc.ToX, ToY: arc.ToY,
			FlightTicks: arc.FlightTicks, PeakHeight: arc.PeakHeight, Gravity: arc.Gravity,
		}
	}

	return Snapshot{
		Tick:        w.tick,
		Player:      PlayerState{ID: uint64(w.player), X: float64(plPos.X), Y: float64(plPos.Y), Strength: strength, MaxDist: maxDist},
		Props:       props,
		Beasts:      beasts,
		Blasts:      blasts,
		Events:      events,
		FieldHalf:   demoFieldHalf,
		Origin:      demoOrigin,
		Selected:    w.selected,
		AimX:        w.aimX,
		AimY:        w.aimY,
		HasAim:      w.hasAim,
		CanThrow:    reason == "",
		BlockReason: reason,
		BlastCount:  w.blastCount,
		PreviewArc:  preview,
	}
}

func propName(i int) string {
	names := []string{"石头", "圆木", "巨石", "铁块"}
	return names[i%len(names)]
}

// 以下几个小包装把组件层常量暴露给 WASM 胶水层（保持胶水层不依赖组件包细节）。
func baseThrowDistance() int { return components.BaseThrowDistance }
func blastRadius() float64   { return components.DefaultBlastRadius }
func blastDamage() int       { return components.DefaultBlastDamage }
func gravity() float64       { return components.DefaultGravity }
