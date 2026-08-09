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
	game "starve/pkg/proto/game"
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
	tick      int64                                // 世界时钟 = tick × dt
	started   bool                                 // 已启动自驱动 tick（防重复 Start）
	players   map[ecs.Entity]string                // 实体 → UID（命令所有权校验）
	pushSink  func(PushEffect)                     // 推送出口（网关注入）；nil 时 PushEffect 丢弃
	saveSink  func([]byte)                         // 存档落盘出口（宿主导入，事件触发用）
	journal   []JournalEntry                       // 指令日志（input journal，随存档保存/重放）
	replay    bool                                 // 重放模式：不追加日志（避免重复记录）
	templates map[components.ItemKind]ItemTemplate // 资源模板表（kind → 静态属性）
	recipes   map[string]Recipe                    // 制作配方表（recipe_id → Recipe）
	config    *GameConfig                          // 世界静态配置（含端上契约）
	mapConfig *game.MapConfig                      // 地形高度场（静态，随存档恢复）
	cmds      *CommandHandler                      // 命令处理（应用逻辑独立文件）
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
	if cfg.CorpseRetentionTicks < 0 {
		cfg.CorpseRetentionTicks = 600 // 10Hz ≈ 1 分钟
	}
	if cfg.InventorySlots <= 0 {
		cfg.InventorySlots = 20
	}
	a := &WorldActor{
		sim:     ecs.NewWorld(),
		cfg:     cfg,
		players: make(map[ecs.Entity]string),
	}
	a.cmds = &CommandHandler{a: a}
	// 组件 codec 注册（快照/存档用）：必须在首次 Add/Query 之前
	components.RegisterCodecs(a.sim)
	// 世界级资源
	a.sim.AddResource(&components.DayCycle{})
	// 玩法系统统一装配（systems.RegisterAll，按域拆分扩展）
	systems.RegisterAll(a.sim, systems.Config{
		GrowthTicks: cfg.GrowthTicks,
	})
	// 集中加载全部配置（资源/模板/配方/工作站），失败则空配置兜底（记录警告）
	gc, err := LoadGameConfig(cfg)
	if err != nil {
		slog.Warn("load game config", "err", err)
	}
	a.templates = gc.Templates
	a.recipes = gc.Recipes
	a.config = gc
	if gc.MapSpec != nil {
		// 地图生成：seed + 规格 → 地形场 + 撒点实体（确定性）
		res := (&MapGenerator{seed: gc.MapSeed, spec: gc.MapSpec}).Generate()
		seedResources(a.sim, res.Resources)
		seedStations(a.sim, res.Stations)
		seedLoot(a.sim, res.Loot)
		a.mapConfig = res.toProto()
	} else {
		// 回退：旧 resources/stations 手摆
		if len(gc.Resources) > 0 {
			seedResources(a.sim, gc.Resources)
		}
		if len(gc.Stations) > 0 {
			seedStations(a.sim, gc.Stations)
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
	case CraftRequest:
		ctx.Respond(a.cmds.craft(m.UID, m.RecipeID))
	case QueryConfig:
		pc := a.config.ToProto()
		pc.Map = a.mapConfig
		ctx.Respond(pc)
	}
}

// QueryConfig 请求世界静态配置（端上契约，登录后推送）。
type QueryConfig struct{}

// CraftRequest 制作请求（request/response）：校验并开始制作。
type CraftRequest struct {
	UID      string
	RecipeID string
}

// CraftResult 制作请求结果。
type CraftResult struct {
	Started bool
	Message string
	Ticks   int
}

// PlayerDisconnect 玩家断线通知（网关注入，触发离线保留）。
type PlayerDisconnect struct {
	UID string
}

// createPlayer 创建玩家实体（位置 + 血量 + 饥饿），登记所有权。
// 重连复用：同 UID 且未死亡的实体直接复用（原地续玩），不新建。
// 注意：复用不要求 Offline 标记——旧连接关闭与 sweeper（1s）之间存在竞态，
// 严格等离线标记会导致重连时创建重复实体（僵尸）。网关在 CreatePlayer 前已踢旧连接，
// 同 UID 只有一个活跃会话，直接复用是安全的。
func (a *WorldActor) createPlayer(uid string) ecs.Entity {
	if e, ok := a.findPlayer(uid); ok {
		if !ecs.Has[components.Dead](a.sim, e) {
			if ecs.Has[components.Offline](a.sim, e) {
				ecs.Remove[components.Offline](a.sim, e)
			}
			a.recordJournal(JournalJoin, uid, 0, nil)
			return e
		}
	}
	e := a.sim.CreateEntity()
	ecs.Add(a.sim, e, components.Position{X: 0, Y: 0})
	ecs.Add(a.sim, e, components.Health{Cur: 100, Max: 100})
	ecs.Add(a.sim, e, components.Hunger{Level: 100, Rate: a.cfg.HungerRate})
	ecs.Add(a.sim, e, components.Player{UID: uid})
	ecs.Add(a.sim, e, components.Inventory{Slots: make([]components.ItemStack, a.cfg.InventorySlots)})
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
	a.completeCrafts()
	a.processDrops()
	a.stampDead()
	a.cleanupCorpses()
	a.cleanupOffline()
	removed := a.drainRemoved()
	dirty := a.sim.DrainDirtySorted()
	delta := DeltaSnapshot(a.sim, dirty, removed)

	// 每 tick 广播增量快照（含昼夜等世界状态）
	a.outbox = append(a.outbox, PushEffect{
		Route:   proto.RouteSnapshotDelta,
		Payload: delta,
	})
	a.drainEffects()
	a.flushOutbox(ctx)
	a.tick++
}

// drainEffects 把世界副作用翻译成 outbox 推送（tick 边界调用）。
// 组件（如 Crafting.Resume）通过 w.Emit 发射意图，不直接依赖 actor/outbox。
func (a *WorldActor) drainEffects() {
	for _, ef := range a.sim.DrainEffects() {
		switch p := ef.(type) {
		case *proto.CraftDone:
			a.outbox = append(a.outbox, PushEffect{Route: proto.RouteCraftDone, Payload: p})
		}
	}
}

// completeCrafts 制作到点：产出（玩家存活才入包）并推送 world.craft.done。
func (a *WorldActor) completeCrafts() {
	var done []ecs.Entity
	ecs.Query[components.Crafting](a.sim, func(e ecs.Entity, c *components.Crafting) {
		if c.TicksLeft <= 0 {
			done = append(done, e)
		}
	})
	for _, e := range done {
		c := ecs.Get[components.Crafting](a.sim, e)
		recipe, ok := a.recipes[c.RecipeID]
		uid := a.players[e]
		success := false
		if ok && a.sim.IsAlive(e) && !ecs.Has[components.Dead](a.sim, e) {
			inv := ecs.Ensure[components.Inventory](a.sim, e)
			t := a.template(recipe.Output.Kind)
			durability := 0
			if t.Tool != nil {
				durability = t.Tool.Durability
			}
			if inv.Add(recipe.Output.Kind, recipe.Output.Count, t.StackSize, durability) >= recipe.Output.Count {
				success = true
			}
			ecs.MarkDirty[components.Inventory](a.sim, e)
		}
		ecs.Remove[components.Crafting](a.sim, e)
		a.outbox = append(a.outbox, PushEffect{
			Route:   proto.RouteCraftDone,
			Payload: &proto.CraftDone{Uid: uid, RecipeId: c.RecipeID, Success: success},
		})
	}
}

// stampDead 给本 tick 新死亡的实体补盖死亡 tick（系统层不知道世界时钟）。
func (a *WorldActor) stampDead() {
	ecs.Query[components.Dead](a.sim, func(e ecs.Entity, d *components.Dead) {
		if d.SinceTick == 0 {
			d.SinceTick = a.tick
			ecs.MarkDirty[components.Dead](a.sim, e)
		}
	})
}

// cleanupCorpses 超过保留时长的尸体销毁（0 = 永久保留）。
func (a *WorldActor) cleanupCorpses() {
	if a.cfg.CorpseRetentionTicks <= 0 {
		return
	}
	var expired []ecs.Entity
	ecs.Query[components.Dead](a.sim, func(e ecs.Entity, d *components.Dead) {
		if d.SinceTick > 0 && a.tick-d.SinceTick >= int64(a.cfg.CorpseRetentionTicks) {
			expired = append(expired, e)
		}
	})
	for _, e := range expired {
		if a.sim.IsAlive(e) {
			a.sim.DestroyEntity(e)
		}
	}
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
		a.cmds.Handle(c)
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
		a.completeCrafts()
		a.processDrops()
		a.stampDead()
		a.cleanupCorpses()
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
	case JournalCraft:
		var id string
		if json.Unmarshal(e.Data, &id) == nil {
			a.cmds.craft(e.UID, id)
		}
	case CommandMove, CommandAttack, CommandGather, CommandPickup, CommandUse, CommandEquip, CommandChop, CommandMine, CommandDrop, CommandCancelCraft, CommandSplit:
		if d := e.decodeData(); d != nil {
			a.commands = append(a.commands, Command{UID: e.UID, Seq: e.Seq, Kind: e.Kind, Data: d})
		}
	}
}

// template 取某 kind 的模板；未配置时返回带默认堆叠上限的空模板。
func (a *WorldActor) template(kind components.ItemKind) ItemTemplate {
	if t, ok := a.templates[kind]; ok {
		return t
	}
	return ItemTemplate{StackSize: 20}
}

// processDrops 死亡掉落：带 Dead 且仍可工作的实体，按模板掉落表生成 Loot，
// 实体就地转为掉落物（移除 Workable 不再可交互；捡走即消失）。
// 植物/石头/生物统一走 Dead，效果差异由模板 drop_table 决定。
func (a *WorldActor) processDrops() {
	var toDrop []ecs.Entity
	ecs.Query[components.Dead](a.sim, func(e ecs.Entity, _ *components.Dead) {
		if ecs.Has[components.Workable](a.sim, e) {
			toDrop = append(toDrop, e)
		}
	})
	for _, e := range toDrop {
		w := ecs.Get[components.Workable](a.sim, e)
		items, err := resolveDropTable(a.template(w.Kind).DropTable)
		if err != nil || len(items) == 0 {
			ecs.Remove[components.Workable](a.sim, e)
			continue
		}
		ecs.Add(a.sim, e, components.Loot{Items: items})
		ecs.Remove[components.Workable](a.sim, e)
	}
}

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
