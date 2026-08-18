package world

import (
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/config"
	"starve/internal/game/systems"
	"starve/internal/game/weather"
	"starve/internal/game/worldmap"
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

// NewWorldActor 创建世界 actor（内部加载配置；简单场景/测试用）。
func NewWorldActor(cfg WorldConfig) *WorldActor {
	gc, err := config.LoadGameConfig(cfg)
	if err != nil {
		slog.Warn("load game config", "err", err)
	}
	return NewWorldActorWithConfig(cfg, gc)
}

// NewWorldActorWithConfig 使用外部已加载的配置构造世界（ConfigManager 场景，避免二次加载）。
func NewWorldActorWithConfig(cfg WorldConfig, gc *GameConfig) *WorldActor {
	return newWorldActor(cfg, gc)
}

func newWorldActor(cfg WorldConfig, gc *GameConfig) *WorldActor {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 50 * time.Millisecond
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
	if cfg.MoveSpeed <= 0 {
		cfg.MoveSpeed = 10 // 默认 10 格/秒
	}
	if cfg.WeatherFrameTicks == 0 {
		cfg.WeatherFrameTicks = 20
	}
	if cfg.OfflineRetentionTicks <= 0 {
		cfg.OfflineRetentionTicks = 6000 // 20Hz ≈ 5 分钟
	}
	if cfg.CorpseRetentionTicks < 0 {
		cfg.CorpseRetentionTicks = 1200 // 20Hz ≈ 1 分钟
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
	components.RegisterCodecs(a.sim, cfg.DebugAOI)
	interactive.RegisterComponents(a.sim) // 交互组件自持注册（本包组件）
	// 世界级资源
	a.sim.AddResource(&components.DayCycle{})
	a.sim.AddResource(&components.DebugFlags{AOI: cfg.DebugAOI})
	a.sim.AddResource(&systems.AOIGrid{Width: 128, Height: 128})
	// 玩法系统统一装配（systems.RegisterAll，按域拆分扩展）
	systems.RegisterAll(a.sim, systems.Config{
		GrowthTicks: cfg.GrowthTicks,
		AOIInterval: cfg.AOIInterval,
	})
	a.templates = gc.Templates
	a.recipes = gc.Recipes
	a.config = gc
	if gc.MapSpec != nil {
		// 地图生成：seed + 规格 → 地形场 + 撒点实体（确定性）
		res := worldmap.NewMapGenerator(gc.MapSeed, gc.MapSpec, gc.Biomes).Generate()
		seedResources(a.sim, res.Resources, a.templates)
		seedStations(a.sim, res.Stations)
		seedLoot(a.sim, res.Loot)
		seedEmitters(a.sim, res.Emitters)
		seedCreatures(a.sim, res.Creatures, gc.Creatures, cfg.TickInterval.Seconds())
		a.mapConfig = res.ToProto()
		// 服务端内部地图数据（地块效果表）作为 ECS 资源：效果系统可直接读取
		a.sim.AddResource(&MapData{
			Width:         res.Width,
			Height:        res.Height,
			SpawnX:        res.SpawnX,
			SpawnY:        res.SpawnY,
			CornerHeights: res.CornerHeights,
			CornerTypes:   res.CornerTypes,
			TileEffects:   res.TileEffects,
			TileParams:    res.TileParams,
			RegionIDs:     res.RegionIDs,
			RegionWeather: res.RegionWeather,
		})
	} else {
		// 回退：旧 resources/stations 手摆
		if len(gc.Resources) > 0 {
			seedResources(a.sim, gc.Resources, a.templates)
		}
		if len(gc.Stations) > 0 {
			seedStations(a.sim, gc.Stations)
		}
	}
	// 动态阻挡层重建：MapData 是唯一地图数据源（地形即时推导 + Block 实体写阻挡层），
	// 启动时全量同步一次（未来种子实体带 Block 也会在这里落进地图）。
	rebuildBlocks(a.sim)
	// 天气资源：相位/季节 + 冷热阈值（默认气候伤害关闭，配置打开）
	wc := gc.Weather
	if wc == nil {
		wc = weather.DefaultConfig()
	}
	a.sim.AddResource(&components.Weather{
		Seed:       gc.MapSeed,
		YearTicks:  wc.YearTicks,
		ColdAt:     wc.ColdAt,
		ColdDamage: wc.ColdDamage,
		HeatAt:     wc.HeatAt,
		HeatDamage: wc.HeatDamage,
	})
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
	case QueryMoveable:
		if ecs.Has[components.Moveable](a.sim, m.Entity) {
			ctx.Respond(*ecs.Get[components.Moveable](a.sim, m.Entity))
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
	case BuildRequest:
		ctx.Respond(a.cmds.build(m.UID, m.Kind))
	case QueryConfig:
		pc := a.config.ToProto()
		pc.Map = a.mapConfig
		ctx.Respond(pc)
	case QueryCanPlace:
		ctx.Respond(a.canPlace(m.Entity, m.X, m.Y))
	}
}

// QueryCanPlace 查询建筑实体能否放到指定位置（客户端幽灵预览用）。
type QueryCanPlace struct {
	Entity ecs.Entity
	X, Y   int
}

func (a *WorldActor) canPlace(e ecs.Entity, x, y int) bool {
	if !ecs.Has[components.Building](a.sim, e) {
		return false
	}
	b := ecs.Get[components.Building](a.sim, e)
	w, h := buildingWH(b)
	md, ok := ecs.TryResource[worldmap.MapData](a.sim)
	return ok && CanPlaceBuilding(md, x, y, w, h)
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

// BuildRequest 建造请求（request/response）：创建未放置的建筑实体。
type BuildRequest struct {
	UID  string
	Kind components.BuildingKind
}

// BuildResult 建造请求结果：Started=true 时 Entity 为新建（未放置）建筑实体。
type BuildResult struct {
	Entity  ecs.Entity
	Started bool
	Message string
}

// PlayerDisconnect 玩家断线通知（网关注入，触发离线保留）。
type PlayerDisconnect struct {
	UID string
}

// createPlayer 创建玩家实体（位置 + 血量 + 饥饿），登记所有权。
// 重连复用：同 UID 的实体一律复用（含已死亡实体），只清离线标记，不复活、不恢复状态——
// "角色还在原地"就是重连的全部语义；死亡/重生流程由后续机制处理。
// 注意：复用不要求 Offline 标记——旧连接关闭与 sweeper（1s）之间存在竞态，
// 严格等离线标记会导致重连时创建重复实体（僵尸）。网关在 CreatePlayer 前已踢旧连接，
// 同 UID 只有一个活跃会话，直接复用是安全的。
func (a *WorldActor) createPlayer(uid string) ecs.Entity {
	if e, ok := a.findPlayer(uid); ok {
		if ecs.Has[components.Offline](a.sim, e) {
			ecs.Remove[components.Offline](a.sim, e)
		}
		a.recordJournal(JournalJoin, uid, 0, nil)
		return e
	}
	e := a.sim.CreateEntity()
	sx, sy := 0, 0
	if md, ok := ecs.TryResource[MapData](a.sim); ok {
		sx, sy = md.SpawnX, md.SpawnY
	}
	ecs.Add(a.sim, e, components.Position{X: sx, Y: sy})
	ecs.Add(a.sim, e, components.Health{Cur: 100, Max: 100})
	ecs.Add(a.sim, e, components.Attackable{})
	ecs.Add(a.sim, e, components.Hunger{Level: 100, Rate: a.cfg.HungerRate})
	ecs.Add(a.sim, e, components.Player{UID: uid})
	ecs.Add(a.sim, e, components.Inventory{Slots: make([]components.ItemStack, a.cfg.InventorySlots)})
	ecs.Add(a.sim, e, components.Effects{Active: map[components.EffectOrder]components.EffectState{}})
	ecs.Add(a.sim, e, components.Moveable{Speed: a.cfg.MoveSpeed})
	ecs.Add(a.sim, e, components.AOI{Radius: defaultAutomateRadius})
	// 裸手默认主动能力：可采集 + 可攻击（砍/挖需装备 Chopper/Miner）
	ecs.Add(a.sim, e, interactive.Picker{Efficiency: 1, Range: 1, Durability: -1})
	ecs.Add(a.sim, e, interactive.Looter{Range: 2})
	ad := a.cfg.AttackDamage
	if ad <= 0 {
		ad = 10
	}
	ecs.Add(a.sim, e, interactive.Attacker{AttackDamage: ad, AttackRange: 2})
	a.players[e] = uid
	a.recordJournal(JournalJoin, uid, 0, nil)
	return e
}

// tileEffectAt 返回 (x,y) 格的地块效果与参数（越界/无地图 = (0,0)）。
// 效果只由服务端结算，不进端上契约（客户端只拿 corner_types 渲染）。
func (a *WorldActor) tileEffectAt(x, y int) (components.EffectOrder, int) {
	if md, ok := ecs.TryResource[MapData](a.sim); ok {
		return md.TileEffectAt(x, y)
	}
	return 0, 0
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
	a.processCreatureDrops()
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
	a.maybePushWeatherFrame()
	a.drainEffects()
	a.flushOutbox(ctx)
	a.tick++
}

// maybePushWeatherFrame 按间隔推一帧天气网格（粗粒度，客户端渲染雾/雨）。
// 采样是确定性纯函数（seed + 相位），重放一致；无地图/关闭时不推。
func (a *WorldActor) maybePushWeatherFrame() {
	if a.cfg.WeatherFrameTicks <= 0 || a.tick%int64(a.cfg.WeatherFrameTicks) != 0 {
		return
	}
	md, ok := ecs.TryResource[MapData](a.sim)
	if !ok {
		return
	}
	wr, ok := ecs.TryResource[components.Weather](a.sim)
	if !ok {
		return
	}
	const cellSize = 10
	cw := (md.Width + cellSize - 1) / cellSize
	ch := (md.Height + cellSize - 1) / cellSize
	frame := &game.WeatherFrame{
		Season:      wr.Season(),
		CellSize:    cellSize,
		CellsPerRow: int32(cw),
	}
	for cy := 0; cy < ch; cy++ {
		for cx := 0; cx < cw; cx++ {
			x, y := cx*cellSize+cellSize/2, cy*cellSize+cellSize/2
			if x >= md.Width {
				x = md.Width - 1
			}
			if y >= md.Height {
				y = md.Height - 1
			}
			h, typ := md.TileAt(x, y)
			smp := weather.SampleAt(a.sim, weather.WeatherQuery{X: x, Y: y, Height: h, TileType: typ, Season: wr.Season(), Tick: wr.Phase})
			frame.Cells = append(frame.Cells, &game.WeatherCell{Fog: smp.Fog, Rain: smp.Rain, Temperature: smp.Temperature})
		}
	}
	// 全局风向/风速（地图中心采样）
	cx, cy := md.Width/2, md.Height/2
	h, typ := md.TileAt(cx, cy)
	wind := weather.SampleAt(a.sim, weather.WeatherQuery{X: cx, Y: cy, Height: h, TileType: typ, Season: wr.Season(), Tick: wr.Phase})
	frame.WindDirX = wind.WindDirX
	frame.WindDirY = wind.WindDirY
	frame.WindSpeed = wind.WindSpeed
	a.outbox = append(a.outbox, PushEffect{Route: proto.RouteWeatherFrame, Payload: frame})
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
	case JournalBuild:
		var kind int32
		if json.Unmarshal(e.Data, &kind) == nil {
			a.cmds.build(e.UID, components.BuildingKind(kind))
		}
	case CommandMove, CommandAttack, CommandGather, CommandPickup, CommandUse, CommandEquip, CommandChop, CommandMine, CommandAutomate, CommandDrop, CommandCancelCraft, CommandSplit, CommandPlace, CommandDemolish:
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

// processDrops 死亡掉落：带 Dead 且仍有受激能力（Choppable/Minable/Pickable）的实体，
// 按模板掉落表生成 Loot，实体就地转为掉落物（移除受激能力不再可交互；捡走即消失）。
// 植物/石头/生物统一走 Dead，效果差异由模板 drop_table 决定。
func (a *WorldActor) processDrops() {
	var toDrop []ecs.Entity
	ecs.Query[components.Dead](a.sim, func(e ecs.Entity, _ *components.Dead) {
		if hasWorkTarget(a.sim, e) {
			toDrop = append(toDrop, e)
		}
	})
	for _, e := range toDrop {
		kind := workTargetKind(a.sim, e)
		items, err := config.ResolveDropTable(a.template(kind).DropTable)
		if err == nil && len(items) > 0 {
			ecs.Add(a.sim, e, components.Lootable{Items: items})
		}
		removeWorkTarget(a.sim, e)
		if ecs.Has[components.Block](a.sim, e) {
			ecs.Remove[components.Block](a.sim, e) // 树/岩倒下：解除占格（实体转掉落物）
		}
	}
}

// hasWorkTarget 实体是否带任一受激工作能力。
func hasWorkTarget(sim *ecs.World, e ecs.Entity) bool {
	return ecs.Has[interactive.Choppable](sim, e) || ecs.Has[interactive.Minable](sim, e) || ecs.Has[interactive.Pickable](sim, e)
}

// workTargetKind 实体受激能力携带的资源 kind。
func workTargetKind(sim *ecs.World, e ecs.Entity) components.ItemKind {
	if ecs.Has[interactive.Choppable](sim, e) {
		return ecs.Get[interactive.Choppable](sim, e).Kind
	}
	if ecs.Has[interactive.Minable](sim, e) {
		return ecs.Get[interactive.Minable](sim, e).Kind
	}
	if ecs.Has[interactive.Pickable](sim, e) {
		return ecs.Get[interactive.Pickable](sim, e).Kind
	}
	return 0
}

// removeWorkTarget 移除全部受激工作能力（实体转掉落物）。
func removeWorkTarget(sim *ecs.World, e ecs.Entity) {
	ecs.Remove[interactive.Choppable](sim, e)
	ecs.Remove[interactive.Minable](sim, e)
	ecs.Remove[interactive.Pickable](sim, e)
}

// processCreatureDrops 生物死亡：按组件掉落表生成 Loot，移除 Creature（尸体由清理系统回收）。
func (a *WorldActor) processCreatureDrops() {
	var dead []ecs.Entity
	ecs.Query[components.Creature](a.sim, func(e ecs.Entity, _ *components.Creature) {
		if ecs.Has[components.Dead](a.sim, e) {
			dead = append(dead, e)
		}
	})
	for _, e := range dead {
		c := ecs.Get[components.Creature](a.sim, e)
		if len(c.Drops) > 0 {
			ecs.Add(a.sim, e, components.Lootable{Items: c.Drops})
		}
		ecs.Remove[components.Creature](a.sim, e)
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

// QueryMoveable 查询实体移动状态（请求-应答，测试/调试用，只读）。
type QueryMoveable struct {
	Entity ecs.Entity
}

// QuerySnapshot 请求全量快照（登录时网关取用，请求-应答）。
type QuerySnapshot struct{}

// CreatePlayer 创建玩家实体并返回实体 ID（登录时使用，请求-应答）。
type CreatePlayer struct {
	UID string
}
