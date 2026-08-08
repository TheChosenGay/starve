package world

import (
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	"starve/pkg/proto"
)

// WorldActor 是 Actor ↔ ECS 的接缝：一个世界 = 一个 WorldActor + 一个 ecs.World。
//
// 纪律（设计文档 §5.2）：
//   - 命令只入缓冲，tick 统一消费（消息到达速率与模拟速率解耦）
//   - ECS 系统是纯函数，副作用只经 outbox，tick 结束统一 drain
//   - 时间由本 actor 注入固定 dt，ECS 不读 wall clock
//   - tick 内禁止 Request(...).Wait()（同步跨 actor 调用）
//
// M5：每 tick 把变更（dirty + 销毁）组装成 SnapshotDelta 广播；
// 登录时通过 QuerySnapshot 下发全量 Snapshot。
type WorldActor struct {
	sim       *ecs.World
	cfg       WorldConfig
	commands  []Command
	outbox    []Effect
	tick      int64                                    // 世界时钟 = tick × dt
	started   bool                                     // 已启动自驱动 tick（防重复 Start）
	players   map[ecs.Entity]string                    // 实体 → UID（命令所有权校验）
	pushSink  func(PushEffect)                         // 推送出口（网关注入）；nil 时 PushEffect 丢弃
	saveSink  func([]byte)                             // 存档落盘出口（宿主导入，事件触发用）
	journal   []JournalEntry                           // 指令日志（input journal，随存档保存/重放）
	replay    bool                                     // 重放模式：不追加日志（避免重复记录）
	templates map[components.ResourceKind]ItemTemplate // 资源模板表（kind → 静态属性）
}

// NewWorldActor 创建世界 actor。
func NewWorldActor(cfg WorldConfig) *WorldActor {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 100 * time.Millisecond
	}
	if cfg.HungerRate < 0 {
		cfg.HungerRate = 1
	}
	if cfg.GrowthTicks <= 0 {
		cfg.GrowthTicks = 20
	}
	if cfg.AttackDamage <= 0 {
		cfg.AttackDamage = 10
	}
	if cfg.OfflineRetentionTicks <= 0 {
		cfg.OfflineRetentionTicks = 3000 // 10Hz ≈ 5 分钟
	}
	a := &WorldActor{
		sim:     ecs.NewWorld(),
		cfg:     cfg,
		players: make(map[ecs.Entity]string),
	}
	// 组件 codec 注册（快照/存档用）：必须在首次 Add/Query 之前
	components.RegisterCodecs(a.sim)
	// 世界级资源
	a.sim.AddResource(&components.DayCycle{})
	// 玩法系统统一装配（systems.RegisterAll，按域拆分扩展）
	systems.RegisterAll(a.sim, systems.Config{
		GrowthTicks: cfg.GrowthTicks,
	})
	// 资源模板表（采集堆叠/掉落/使用效果）
	if cfg.TemplatesPath != "" {
		if ts, err := loadTemplates(cfg.TemplatesPath); err != nil {
			slog.Warn("load resource templates", "path", cfg.TemplatesPath, "err", err)
		} else {
			a.templates = ts
		}
	}
	// 资源配置 seed（配置驱动，缺失/出错则跳过）
	if cfg.ResourcesPath != "" {
		if seeds, err := loadResourceSeeds(cfg.ResourcesPath); err != nil {
			slog.Warn("load resources config", "path", cfg.ResourcesPath, "err", err)
		} else {
			seedResources(a.sim, seeds)
		}
	}
	return a
}

// SetPushSink 注入推送出口（网关注册，把 PushEffect 转成客户端推送）。
// 需在世界 actor 启动（Start）前调用；只会在世界处理 goroutine 上被访问。
func (a *WorldActor) SetPushSink(fn func(ef PushEffect)) { a.pushSink = fn }

// SetSaveSink 注入存档落盘出口（宿主写文件）。
// 事件触发的自动存档（如每天开始）会调用它；手动存档直接调 Save() 自己落盘。
func (a *WorldActor) SetSaveSink(fn func(data []byte)) { a.saveSink = fn }

// WorldTime 返回当前世界时钟（= tick × dt）。
func (a *WorldActor) WorldTime() time.Duration {
	return time.Duration(a.tick) * a.cfg.TickInterval
}

func (a *WorldActor) Receive(ctx actor.IActorContext) {
	switch m := ctx.Message().(type) {
	case Start:
		if !a.started {
			a.started = true
			ctx.SendRepeat(ctx.PID(), Tick{}, a.cfg.TickInterval)
		}
	case Command:
		a.commands = append(a.commands, m) // 只入缓冲，不立即执行
	case Tick:
		a.onTick(ctx)
	case QueryPosition:
		if ecs.Has[components.Position](a.sim, m.Entity) {
			ctx.Respond(*ecs.Get[components.Position](a.sim, m.Entity))
		} else {
			ctx.Respond(nil)
		}
	case QueryWorldTime:
		ctx.Respond(a.WorldTime())
	case QuerySnapshot:
		ctx.Respond(FullSnapshot(a.sim))
	case SaveRequest:
		ctx.Respond(a.Save())
	case CreatePlayer:
		// MVP：登录时在 tick 外直接创建（结构变更走命令缓冲的纪律在 M5 收拢）
		ctx.Respond(a.createPlayer(m.UID))
	case PlayerDisconnect:
		a.markOffline(m.UID)
	}
}

// PlayerDisconnect 玩家断线通知（网关注入，触发离线保留）。
type PlayerDisconnect struct {
	UID string
}

// createPlayer 创建玩家实体（位置 + 血量 + 饥饿），登记所有权。
// 重连复用：同 UID 且未死亡、仍离线保留的实体直接恢复在线（原地续玩），不新建。
func (a *WorldActor) createPlayer(uid string) ecs.Entity {
	if e, ok := a.findPlayer(uid); ok {
		if !ecs.Has[components.Dead](a.sim, e) && ecs.Has[components.Offline](a.sim, e) {
			ecs.Remove[components.Offline](a.sim, e)
			a.recordJournal(JournalJoin, uid, 0, nil)
			return e
		}
	}
	e := a.sim.CreateEntity()
	ecs.Add(a.sim, e, components.Position{X: 0, Y: 0})
	ecs.Add(a.sim, e, components.Health{Cur: 100, Max: 100})
	ecs.Add(a.sim, e, components.Hunger{Level: 100, Rate: a.cfg.HungerRate})
	ecs.Add(a.sim, e, components.Player{UID: uid})
	ecs.Add(a.sim, e, components.Inventory{Items: map[components.ResourceKind]components.ItemStack{}})
	a.players[e] = uid
	a.recordJournal(JournalJoin, uid, 0, nil)
	return e
}

// findPlayer 按 UID 查玩家实体（遍历 Player 组件；玩家量小，够用）。
func (a *WorldActor) findPlayer(uid string) (ecs.Entity, bool) {
	var found ecs.Entity
	ok := false
	ecs.Query[components.Player](a.sim, func(e ecs.Entity, p *components.Player) {
		if !ok && p.UID == uid {
			found = e
			ok = true
		}
	})
	return found, ok
}

// markOffline 玩家断线：实体保留在世界（挂 Offline），供重连复用/超时清理。
// 已死亡或已离线的实体不重复标记。
func (a *WorldActor) markOffline(uid string) {
	e, ok := a.findPlayer(uid)
	if !ok || ecs.Has[components.Dead](a.sim, e) || ecs.Has[components.Offline](a.sim, e) {
		return
	}
	ecs.Add(a.sim, e, components.Offline{SinceTick: a.tick})
	a.recordJournal(JournalDisconnect, uid, 0, nil)
}

// cleanupOffline 清理超过保留时长的离线实体（销毁并广播移除）。
func (a *WorldActor) cleanupOffline() {
	var expired []ecs.Entity
	ecs.Query[components.Offline](a.sim, func(e ecs.Entity, o *components.Offline) {
		if a.tick-o.SinceTick >= int64(a.cfg.OfflineRetentionTicks) {
			expired = append(expired, e)
		}
	})
	for _, e := range expired {
		if uid, ok := a.players[e]; ok {
			a.recordJournal(JournalDestroy, uid, 0, nil)
		}
		if a.sim.IsAlive(e) {
			a.sim.DestroyEntity(e)
		}
	}
}

// onTick：命令 → 系统 → 快照 → outbox。
func (a *WorldActor) onTick(ctx actor.IActorContext) {
	a.applyCommands()
	a.sim.RunSystems(a.cfg.TickInterval)
	a.processDrops()
	a.cleanupOffline()
	removed := a.drainRemoved()
	dirty := a.sim.DrainDirtySorted()
	delta := DeltaSnapshot(a.sim, dirty, removed)

	// 每 tick 广播增量快照（含昼夜等世界状态）
	a.outbox = append(a.outbox, PushEffect{
		Route:   proto.RouteSnapshotDelta,
		Payload: delta,
	})
	a.flushOutbox(ctx)
	a.tick++
}

// drainRemoved 消费本 tick 的实体销毁事件，并清理玩家所有权表。
func (a *WorldActor) drainRemoved() []ecs.Entity {
	var removed []ecs.Entity
	for _, ev := range a.sim.DrainEvents() {
		if ev.Kind == ecs.EntityDestroyed {
			removed = append(removed, ev.Entity)
			delete(a.players, ev.Entity)
		}
	}
	return removed
}

// applyCommands 校验并执行缓冲中的命令（游戏语义 → ECS 操作）。
func (a *WorldActor) applyCommands() {
	for _, c := range a.commands {
		switch c.Kind {
		case CommandMove:
			a.applyMove(c)
		case CommandAttack:
			a.applyAttack(c)
		case CommandGather:
			a.applyGather(c)
		case CommandPickup:
			a.applyPickup(c)
		case CommandUse:
			a.applyUse(c)
		}
		a.recordJournal(c.Kind, c.UID, c.Seq, c.Data)
	}
	a.commands = a.commands[:0]
}

// recordJournal 记录一条指令日志（重放模式跳过，避免重复记录）。
func (a *WorldActor) recordJournal(kind CommandKind, uid string, seq uint64, data any) {
	if a.replay {
		return
	}
	e := JournalEntry{Tick: a.tick, UID: uid, Seq: seq, Kind: kind}
	if data != nil {
		if raw, err := json.Marshal(data); err == nil {
			e.Data = raw
		}
	}
	a.journal = append(a.journal, e)
}

// Replay 从（应为全新/与原始同配置的）世界按指令日志重放，推进到存档 tick。
// 验收：重放后的 FullSnapshot 应等于同 tick 保存的全量快照（确定性模拟）。
func (a *WorldActor) Replay(entries []JournalEntry, untilTick int64) {
	a.replay = true
	defer func() { a.replay = false }()

	byTick := make(map[int64][]JournalEntry)
	var ticks []int64
	for _, e := range entries {
		if _, ok := byTick[e.Tick]; !ok {
			ticks = append(ticks, e.Tick)
		}
		byTick[e.Tick] = append(byTick[e.Tick], e)
	}
	sort.Slice(ticks, func(i, j int) bool { return ticks[i] < ticks[j] })

	// 每个相位（0..untilTick-1）：先应用该相位事件，再跑一轮系统（与真实世界一致）。
	for t := int64(0); t < untilTick; t++ {
		a.tick = t
		for _, e := range byTick[t] {
			a.applyEntry(e)
		}
		a.applyCommands()
		a.sim.RunSystems(a.cfg.TickInterval)
		a.processDrops()
	}
	// 保存 tick 之后（保存前）到达的事件：应用但不推进系统。
	for _, t := range ticks {
		if t < untilTick {
			continue
		}
		for _, e := range byTick[t] {
			a.applyEntry(e)
		}
	}
	a.tick = untilTick
}

// applyEntry 应用一条日志事件（重放专用）。
func (a *WorldActor) applyEntry(e JournalEntry) {
	switch e.Kind {
	case JournalJoin:
		a.createPlayer(e.UID)
	case JournalDisconnect:
		a.markOffline(e.UID)
	case JournalDestroy:
		if ent, ok := a.findPlayer(e.UID); ok && a.sim.IsAlive(ent) {
			a.sim.DestroyEntity(ent)
		}
	case CommandMove, CommandAttack, CommandGather, CommandPickup, CommandUse:
		if d := e.decodeData(); d != nil {
			a.commands = append(a.commands, Command{UID: e.UID, Seq: e.Seq, Kind: e.Kind, Data: d})
		}
	}
}

func (a *WorldActor) applyGather(c Command) {
	g, ok := c.Data.(GatherData)
	if !ok {
		return
	}
	if a.players[g.Player] != c.UID {
		return // 只能控制自己的实体
	}
	if !ecs.Has[components.Gatherable](a.sim, g.Target) {
		return // 目标不可采集
	}
	if !ecs.Has[components.Position](a.sim, g.Player) || !ecs.Has[components.Position](a.sim, g.Target) {
		return
	}
	if !ecs.Get[components.Position](a.sim, g.Player).WithinRange(*ecs.Get[components.Position](a.sim, g.Target), 2) {
		return // 距离不够
	}
	gt := ecs.Get[components.Gatherable](a.sim, g.Target)
	if gt.Count <= 0 {
		return // 已耗尽
	}

	// 玩家资源 +1（首次采集自动建背包）
	inv := a.ensureInventory(g.Player)
	a.addItem(inv, gt.Kind, 1)
	ecs.MarkDirty[components.Inventory](a.sim, g.Player)

	// 目标 Count-1；耗尽则移除 Gatherable（实体保留，客户端看到组件消失）
	if gt.Count == 1 {
		ecs.Remove[components.Gatherable](a.sim, g.Target)
	} else {
		ecs.Set(a.sim, g.Target, components.Gatherable{Kind: gt.Kind, Count: gt.Count - 1})
	}
}

// ensureInventory 返回玩家背包；缺失时补一个空背包。
func (a *WorldActor) ensureInventory(e ecs.Entity) *components.Inventory {
	if !ecs.Has[components.Inventory](a.sim, e) {
		ecs.Add(a.sim, e, components.Inventory{Items: map[components.ResourceKind]components.ItemStack{}})
	}
	return ecs.Get[components.Inventory](a.sim, e)
}

// addItem 给背包加物品（按模板堆叠上限；MVP 超上限截断）。
func (a *WorldActor) addItem(inv *components.Inventory, kind components.ResourceKind, count int) {
	if inv.Items == nil {
		inv.Items = map[components.ResourceKind]components.ItemStack{}
	}
	cur := inv.Items[kind]
	if cur.Count == 0 {
		cur = components.ItemStack{Kind: kind, MaxStack: a.template(kind).StackSize}
	}
	cur.Count += count
	if cur.MaxStack > 0 && cur.Count > cur.MaxStack {
		cur.Count = cur.MaxStack
	}
	inv.Items[kind] = cur
}

// template 取某 kind 的模板；未配置时返回带默认堆叠上限的空模板。
func (a *WorldActor) template(kind components.ResourceKind) ItemTemplate {
	if t, ok := a.templates[kind]; ok {
		return t
	}
	return ItemTemplate{StackSize: 20}
}

// processDrops 死亡掉落：带 Dead 且仍可采集的实体，按模板掉落表生成 Loot，
// 实体就地转为掉落物（移除 Gatherable 不再可采集；捡走即消失）。
// 植物/石头/生物统一走 Dead，效果差异由模板 drop_table 决定。
func (a *WorldActor) processDrops() {
	var toDrop []ecs.Entity
	ecs.Query[components.Dead](a.sim, func(e ecs.Entity, _ *components.Dead) {
		if ecs.Has[components.Gatherable](a.sim, e) {
			toDrop = append(toDrop, e)
		}
	})
	for _, e := range toDrop {
		g := ecs.Get[components.Gatherable](a.sim, e)
		items, err := resolveDropTable(a.template(g.Kind).DropTable)
		if err != nil || len(items) == 0 {
			ecs.Remove[components.Gatherable](a.sim, e)
			continue
		}
		ecs.Add(a.sim, e, components.Loot{Items: items})
		ecs.Remove[components.Gatherable](a.sim, e)
	}
}

// applyPickup 拾取掉落物：Loot 物品并入背包，掉落实体销毁。
func (a *WorldActor) applyPickup(c Command) {
	p, ok := c.Data.(PickupData)
	if !ok {
		return
	}
	if a.players[p.Player] != c.UID {
		return // 只能控制自己的实体
	}
	if !ecs.Has[components.Loot](a.sim, p.Target) || !a.sim.IsAlive(p.Target) {
		return
	}
	if !ecs.Has[components.Position](a.sim, p.Player) || !ecs.Has[components.Position](a.sim, p.Target) {
		return
	}
	if !ecs.Get[components.Position](a.sim, p.Player).WithinRange(*ecs.Get[components.Position](a.sim, p.Target), 2) {
		return // 距离不够
	}
	loot := ecs.Get[components.Loot](a.sim, p.Target)
	inv := a.ensureInventory(p.Player)
	for _, s := range loot.Items {
		a.addItem(inv, s.Kind, s.Count)
	}
	ecs.MarkDirty[components.Inventory](a.sim, p.Player)
	a.sim.DestroyEntity(p.Target)
}

// applyUse 使用物品：消耗背包一个该物品，按模板 use_effect 作用到玩家。
func (a *WorldActor) applyUse(c Command) {
	u, ok := c.Data.(UseData)
	if !ok {
		return
	}
	if a.players[u.Player] != c.UID {
		return
	}
	t, ok := a.templates[u.Kind]
	if !ok || t.UseEffect == nil {
		return // 该物品不可使用
	}
	inv := a.ensureInventory(u.Player)
	cur, ok := inv.Items[u.Kind]
	if !ok || cur.Count <= 0 {
		return
	}
	cur.Count--
	if cur.Count <= 0 {
		delete(inv.Items, u.Kind)
	} else {
		inv.Items[u.Kind] = cur
	}
	ecs.MarkDirty[components.Inventory](a.sim, u.Player)

	if ef := t.UseEffect; ef.Hunger != 0 {
		h := ecs.Get[components.Hunger](a.sim, u.Player)
		h.Level += ef.Hunger
		if h.Level < 0 {
			h.Level = 0
		}
		if h.Level > 100 {
			h.Level = 100
		}
		ecs.MarkDirty[components.Hunger](a.sim, u.Player)
	}
	if ef := t.UseEffect; ef.Health != 0 {
		hp := ecs.Get[components.Health](a.sim, u.Player)
		hp.Cur += ef.Health
		if hp.Cur < 0 {
			hp.Cur = 0
		}
		if hp.Cur > hp.Max {
			hp.Cur = hp.Max
		}
		ecs.MarkDirty[components.Health](a.sim, u.Player)
	}
}

func (a *WorldActor) applyMove(c Command) {
	m, ok := c.Data.(MoveData)
	if !ok {
		return // 非法命令：丢弃
	}
	if a.players[m.Entity] != c.UID {
		return // 只能移动自己的实体
	}
	if !ecs.Has[components.Position](a.sim, m.Entity) {
		return
	}
	p := ecs.Get[components.Position](a.sim, m.Entity)
	ecs.Set(a.sim, m.Entity, components.Position{X: p.X + m.DX, Y: p.Y + m.DY})
}

func (a *WorldActor) applyAttack(c Command) {
	at, ok := c.Data.(AttackData)
	if !ok {
		return
	}
	if a.players[at.Attacker] != c.UID {
		return // 只能控制自己的实体
	}
	if !ecs.Has[components.Health](a.sim, at.Target) {
		return
	}
	if !ecs.Has[components.Position](a.sim, at.Attacker) || !ecs.Has[components.Position](a.sim, at.Target) {
		return
	}
	if !ecs.Get[components.Position](a.sim, at.Attacker).WithinRange(*ecs.Get[components.Position](a.sim, at.Target), 2) {
		return // 距离不够
	}
	hp := ecs.Get[components.Health](a.sim, at.Target)
	ecs.Set(a.sim, at.Target, components.Health{Cur: hp.Cur - a.cfg.AttackDamage, Max: hp.Max})
}

// flushOutbox 统一执行副作用（发送/推送/存档）。
func (a *WorldActor) flushOutbox(ctx actor.IActorContext) {
	for _, ef := range a.outbox {
		switch e := ef.(type) {
		case PushEffect:
			if a.pushSink != nil {
				a.pushSink(e)
			}
		case SendMessageEffect:
			ctx.Send(e.To, e.Msg)
		}
	}
	a.outbox = a.outbox[:0]
}

// QueryPosition 查询实体位置（请求-应答，供外部/网关/测试使用）。
type QueryPosition struct {
	Entity ecs.Entity
}

// QuerySnapshot 请求全量快照（登录时网关取用，请求-应答）。
type QuerySnapshot struct{}

// CreatePlayer 创建玩家实体并返回实体 ID（登录时使用，请求-应答）。
type CreatePlayer struct {
	UID string
}
