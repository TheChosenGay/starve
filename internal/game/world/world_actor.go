package world

import (
	"encoding/json"
	"log/slog"
	"sort"
	"time"

	"github.com/TheChosenGay/actor"
	"starve/internal/ecs"
	"starve/internal/game/collision"
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
// 登录 QuerySnapshot 下发该玩家视野基线；tick 收尾按玩家裁剪 SnapshotDelta。
// 存档/回放仍用全图 FullSnapshot。
type WorldActor struct {
	sim           *ecs.World
	cfg           WorldConfig
	commands      []Command
	outbox        []Effect
	tick          int64                                  // 世界时钟 = tick × dt
	started       bool                                   // 已启动自驱动 tick（防重复 Start）
	tickRepeater  actor.ISendRepeater                    // tick 定时器（Shutdown 时停止）
	tickStartWall time.Time                              // 自驱动 tick 的基准墙钟（算"落后多少"用）
	lastCatchup   int                                    // 上一 tick 因追步多跑的步数（观测用）
	players       map[ecs.Entity]string                  // 实体 → UID（命令所有权校验）
	pushSink      func(PushEffect)                       // 推送出口（网关注入）；nil 时 PushEffect 丢弃
	saveSink      func([]byte) error                     // 存档落盘出口（宿主导入，事件触发用）
	journal       []JournalEntry                         // 指令日志（input journal，随存档保存/重放）
	replay        bool                                   // 重放模式：不追加日志（避免重复记录）
	templates     map[components.ItemKind]ItemTemplate   // 资源模板表（kind → 静态属性）
	recipes       map[string]Recipe                      // 制作配方表（recipe_id → Recipe）
	config        *GameConfig                            // 世界静态配置（含端上契约）
	drops         *DropProcessor                         // 独立掉落编排：上下文、规则、位置与 Loot 实体
	mapConfig     *game.MapConfig                        // 地形高度场（静态，随存档恢复）
	blockers      *blockerIndex                          // 占位写入目标（Block 钩子用：放置冲突 + 寻路代价）
	collides      *collideIndex                          // 碰撞形状写入目标（Collide 钩子用：形状层）
	creatureTiles *creatureOccupancy                     // 动物占格跟踪（放置冲突用；每 tick 同步）
	cmds          *CommandHandler                        // 命令处理（应用逻辑独立文件）
	observer      TickObserver                           // tick 观测出口（不参与模拟）
	stats         TickStats                              // 上一 tick 的观测（只在 tick 线程读写）
	saveObserver  SaveObserver                           // save 观测出口（不参与存档语义）
	inputAcks     map[string]InputAck                    // UID → 当前输入世代与**已消费**（已烘进快照自己状态）的最大 seq
	inputReceived map[string]uint64                      // UID → **已收到**的最大 seq（去重/排序用；不对外）
	interest      map[ecs.Entity]map[ecs.Entity]struct{} // 玩家 → 上次已下发实体；会话级，不进存档
}

// maxTickDebt 是世界时钟最多允许落后墙钟多少个 tick；超过就丢（见 dropOverdueTicks）。
//
// 4 个 tick = 200ms：正常抖动/单次 GC 不会触发；真的过载时才开始丢，且丢完立刻追平。
const maxTickDebt = 4

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
	if cfg.NpcCorpseRetentionTicks <= 0 {
		// NPC 尸体默认只留 10 秒：够玩家看到/反应，又不至于长期占实体。
		// 想调回旧行为可显式设置该字段（或 GATE_CORPSE_SECONDS）。
		cfg.NpcCorpseRetentionTicks = 200
	}
	if cfg.InventorySlots <= 0 {
		cfg.InventorySlots = 20
	}
	if cfg.ViewRadius == 0 {
		cfg.ViewRadius = config.DefaultViewRadius
	}
	a := &WorldActor{
		sim:           ecs.NewWorld(),
		cfg:           cfg,
		players:       make(map[ecs.Entity]string),
		inputAcks:     make(map[string]InputAck),
		inputReceived: make(map[string]uint64),
		interest:      make(map[ecs.Entity]map[ecs.Entity]struct{}),
	}
	a.cmds = &CommandHandler{a: a}
	// 组件 codec 注册（快照/存档用）：必须在首次 Add/Query 之前
	components.RegisterCodecs(a.sim, cfg.DebugAOI)
	interactive.RegisterComponents(a.sim) // 交互组件自持注册（本包组件）
	// 世界级资源
	a.sim.AddResource(&components.DayCycle{})
	a.sim.AddResource(&components.DebugFlags{AOI: cfg.DebugAOI, Collision: cfg.DebugCollision})
	a.sim.AddResource(&systems.AOIGrid{Width: 128, Height: 128})
	a.sim.AddResource(&systems.ControlQueue{})
	a.sim.AddResource(&systems.ActionCommitQueue{})
	a.sim.AddResource(systems.NewActionExecutorRegistry())
	a.sim.AddResource(&components.ActionMetrics{})
	a.sim.AddResource(&components.TickEventBuffer{})
	// Boss 动作队列（行为树产出意图、世界层消费）；演示与玩法都从这里取。
	a.sim.AddResource(&components.BossActionQueue{})
	// 碰撞形状层与占位层分开装配（这次重构的核心拆分）：
	//   - collision.Index 是形状层（Collide 组件驱动）；
	//   - blockerIndex 是占位层（Block 组件驱动）。
	// 两者都必须在实体创建之前就位——组件的挂载钩子要能找到写入目标。
	a.collides = newCollideIndex(collision.NewIndex())
	a.sim.AddResource(a.collides.index) // MoveSystem 从这里做扫掠 + 滑动
	a.sim.AddResource(a.collides)
	a.blockers = newBlockerIndex()
	a.sim.AddResource(a.blockers)
	// 动物占格层：动物算占格（不能把东西放在动物身上），且动物会移动，
	// 所以单独跟踪"每只动物占的格"，跨格时精确清旧格（见 creature_occupancy.go）。
	a.creatureTiles = newCreatureOccupancy()
	// 玩法系统统一装配（systems.RegisterAll，按域拆分扩展）
	systems.RegisterAll(a.sim, systems.Config{
		GrowthTicks: cfg.GrowthTicks,
		AOIInterval: cfg.AOIInterval,
	})
	// 动物占格同步：order 97（移动 95 / DebugShape 96 之后），保证放置校验读到最新占格。
	a.sim.AddSystem(97, NewCreatureOccupancySystem(a.creatureTiles))
	// Boss 动作消费者：order 99（Throw 98 之后、Hunger 100 之前）。
	//
	// 行为树只产出意图（components.EmitBossAction），真实世界层必须有消费者，
	// 否则 Boss 的投弹/锤地在正式玩法里全是空放（此前只有 cmd/bossdemo 自己消费）。
	// order 见 boss_action.go 的说明：95~98 已被占满，冲突会报错。
	a.sim.AddSystem(99, NewBossActionSystem(a))
	a.templates = gc.Templates
	a.recipes = gc.Recipes
	a.config = gc
	a.drops = NewDropProcessor(a.sim, config.TableDropResolver{
		Templates: gc.Templates,
		Creatures: gc.Creatures,
		Biomes:    gc.Biomes,
	}, gc.MapSeed)
	if gc.MapSpec != nil {
		// 地图生成：seed + 规格 → 地形场 + 撒点实体（确定性）
		res := worldmap.NewMapGenerator(gc.MapSeed, gc.MapSpec, gc.Biomes).Generate()
		seedResources(a.sim, res.Resources, a.templates)
		seedStations(a.sim, res.Stations)
		seedRevivalStatues(a.sim, res.RevivalStatues)
		seedLoot(a.sim, res.Loot)
		seedEmitters(a.sim, res.Emitters)
		seedCreatures(a.sim, res.Creatures, gc.Creatures, cfg.TickInterval.Seconds())
		a.mapConfig = res.ToProto()
		// 服务端内部地图数据（地块效果表）作为 ECS 资源：效果系统可直接读取。
		// attachMap 同时按已生成的种子实体重建形状碰撞层（实心格层）。
		a.attachMap(&MapData{
			Width:         res.Width,
			Height:        res.Height,
			SpawnX:        res.SpawnX,
			SpawnY:        res.SpawnY,
			CornerHeights: res.CornerHeights,
			CornerTypes:   res.CornerTypes,
			TileEffects:   res.TileEffects,
			TileParams:    res.TileParams,
			RegionIDs:     res.RegionIDs,
			RegionBiomes:  res.RegionBiomes,
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
	// 无地图兜底：没有 MapData 时 attachMap 不会触发对账，这里按实体把两层建起来
	//（占位层要等地图就绪，没有地图就没有寻路，但形状层不依赖地图）。
	if _, ok := ecs.TryResource[MapData](a.sim); !ok {
		rebuildBlockers(a.sim, a.blockers)
		rebuildCollides(a.sim, a.collides)
	}
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
func (a *WorldActor) SetSaveSink(fn func(data []byte) error) { a.saveSink = fn }

// SetTickObserver 注入 tick 观测器；只报告数据，不允许反向修改世界。
func (a *WorldActor) SetTickObserver(observer TickObserver) { a.observer = observer }

// SetSaveObserver 注入存档观测器；只报告数据，不改变保存结果。
func (a *WorldActor) SetSaveObserver(observer SaveObserver) { a.saveObserver = observer }

// WorldTime 返回当前世界时钟（= tick × dt）。
func (a *WorldActor) WorldTime() time.Duration {
	return time.Duration(a.tick) * a.cfg.TickInterval
}

func (a *WorldActor) Receive(ctx actor.IActorContext) {
	switch m := ctx.Message().(type) {
	case Start:
		if !a.started {
			a.started = true
			a.tickStartWall = time.Now()
			a.tickRepeater = ctx.SendRepeat(ctx.PID(), Tick{}, a.cfg.TickInterval)
		}
	case Shutdown:
		if a.tickRepeater != nil {
			a.tickRepeater.Stop()
			a.tickRepeater = nil
		}
		a.started = false
	case Command:
		a.commands = append(a.commands, m) // 只入缓冲，不立即执行
	case BeginInputEpoch:
		if m.UID != "" && m.Epoch != 0 {
			a.inputAcks[m.UID] = InputAck{Epoch: m.Epoch}
			a.inputReceived[m.UID] = 0
			// 输入世代换了 ⇒ 客户端的序号重新从 1 开始：清掉该玩家的待消费队列与序号记账，
			// 否则"按序号连续消费"会拿旧世代的序号当基线，一直等一个永远不来的缺口。
			if player, ok := a.findPlayer(m.UID); ok && player != 0 {
				if q, ok := ecs.TryResource[systems.ControlQueue](a.sim); ok {
					q.ResetActor(player)
				}
			}
		}
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
		snap := a.viewSnapshot(m.UID)
		snap.Tick = uint64(a.tick)
		ctx.Respond(snap)
	case SaveRequest:
		ctx.Respond(a.SaveWithTrigger(m.Trigger))
	case CreatePlayer:
		// MVP：登录时在 tick 外直接创建（结构变更走命令缓冲的纪律在 M5 收拢）
		ctx.Respond(a.createPlayer(m.UID))
	case PlayerDisconnect:
		a.markOffline(m.UID)
		delete(a.inputAcks, m.UID)
		delete(a.inputReceived, m.UID)
	case CraftRequest:
		if !a.acceptsInputIdentity(m.UID, m.InputEpoch, m.Seq) {
			ctx.Respond(CraftResult{Message: "stale input"})
			break
		}
		result := a.cmds.preflightCraft(m.UID, m.RecipeID)
		if result.Started {
			player, _ := a.findPlayer(m.UID)
			a.commands = append(a.commands, Command{
				UID:        m.UID,
				InputEpoch: m.InputEpoch,
				Seq:        m.Seq,
				RequestID:  m.RequestID,
				Kind:       CommandCraft,
				Data:       CraftData{Player: player, RecipeID: m.RecipeID},
			})
		}
		ctx.Respond(result)
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
	UID        string
	RecipeID   string
	Seq        uint64
	InputEpoch uint64
	RequestID  uint64
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
		a.recordJournal(JournalJoin, uid, 0, 0, nil)
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
	ecs.Add(a.sim, e, components.Moveable{
		Speed:          a.cfg.MoveSpeed,
		EffectiveSpeed: a.cfg.MoveSpeed,
	})
	// 玩家是动态实体；碰撞体独立挂 Collide（半径由客户端模型推导，configs/models.json player 条目）
	ecs.Add(a.sim, e, components.Collide{
		Shape:      components.CollideShapeCapsule,
		Radius:     systems.BodyRadius,
		BodyHeight: systems.BodyHeight,
	})
	ecs.Add(a.sim, e, components.AOI{Radius: defaultAutomateRadius})
	// 裸手默认主动能力：可采集 + 可攻击（砍/挖需装备 Chopper/Miner）
	ecs.Add(a.sim, e, interactive.Picker{Efficiency: 1, Range: 1, Durability: -1})
	ecs.Add(a.sim, e, interactive.Looter{Range: 2})
	ad := a.cfg.AttackDamage
	if ad <= 0 {
		ad = 10
	}
	ecs.Add(a.sim, e, interactive.Attacker{AttackDamage: ad, AttackRange: 2})
	// 投掷能力：力量决定最大投掷距离（距离 = 基础距离 × 力量 / 质量）。
	// 挂在玩家自身（真实实现里也可来自手持装备，ActorCap 会优先取手部）。
	strength := a.cfg.ThrowStrength
	if strength <= 0 {
		strength = defaultThrowStrength
	}
	ecs.Add(a.sim, e, interactive.Thrower{Strength: strength})
	// 出生赠送炸弹（可投掷物）。没有炸弹就没法测投掷——
	// 而世界里目前没有自然产出的爆炸物，所以直接给。
	if n := a.cfg.StartingBombs; n > 0 {
		inv := ecs.Get[components.Inventory](a.sim, e)
		inv.Add(components.ItemBomb, n, bombStackSize, 0)
	}
	a.players[e] = uid
	a.recordJournal(JournalJoin, uid, 0, 0, nil)
	return e
}

// defaultThrowStrength 是玩家未配置时的投掷力量。
//
// 取 20：与炸弹质量（见 resource_templates.json）相除后，
// 投掷距离约 8~10 格 —— 够越过一屏内的小段距离，又不至于随手扔出视野。
const defaultThrowStrength = 20

// bombStackSize 是炸弹的堆叠上限（与模板保持一致，避免两处不一致）。
const bombStackSize = 5

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
// 死亡玩家也挂 Offline：重连仍复用原实体，超时后由离线 TTL 回收，避免永久泄漏。
func (a *WorldActor) markOffline(uid string) {
	e, ok := a.findPlayer(uid)
	if !ok || ecs.Has[components.Offline](a.sim, e) {
		return
	}
	ecs.Add(a.sim, e, components.Offline{SinceTick: a.tick})
	a.forgetInterest(e)
	a.recordJournal(JournalDisconnect, uid, 0, 0, nil)
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
			a.recordJournal(JournalDestroy, uid, 0, 0, nil)
			delete(a.players, e)
		}
		a.forgetInterest(e)
		if a.sim.IsAlive(e) {
			a.sim.DestroyEntity(e)
		}
	}
}

// onTick：命令 → 系统 → 快照 → outbox。
func (a *WorldActor) onTick(ctx actor.IActorContext) {
	startedAt := time.Now()
	a.dropOverdueTicks(startedAt)
	commandCount := len(a.commands)
	components.BeginTickEvents(a.sim, a.tick)
	a.applyCommands()
	a.sim.RunSystems(a.cfg.TickInterval)
	a.commitConsumedInputs() // ⚠️ 必须在系统跑完之后：ACK 语义 = "已烘进本 tick 的自己状态"
	a.cmds.applyActionCommits()
	a.completeCrafts()
	a.processDrops()
	a.stampDead()
	a.cleanupCorpses()
	a.cleanupOffline()
	events := components.DrainTickEvents(a.sim)
	removed := a.drainRemoved()
	dirty := a.sim.DrainDirtySorted()
	projStart := time.Now()
	proj := a.pushInterestDeltas(dirty, removed, events)
	projDur := time.Since(projStart)
	a.maybePushWeatherFrame()
	a.drainEffects()
	effectCount := len(a.outbox)
	a.flushOutbox(ctx)
	// 输入队列/追步观测：积压多少、追了多少步、丢了多少（过载时先看这三个）。
	backlog, maxBacklog := 0, 0
	catchup := 0
	if q, ok := ecs.TryResource[systems.ControlQueue](a.sim); ok {
		backlog, maxBacklog = q.Backlog()
		catchup = q.CatchupExtra
		a.stats.DroppedOps = q.Dropped
		a.stats.Desyncs = q.Desyncs
	}
	actionEvents := a.drainActionStats()
	impactEvents, healthEvents := domainEventStats(events)
	if a.observer != nil {
		a.observer.ObserveTick(TickStats{
			Tick:               a.tick,
			Duration:           time.Since(startedAt),
			ProjectionDuration: projDur,
			ViewScanDuration:   proj.scan,
			ViewEncodeDuration: proj.encode,
			Commands:           commandCount,
			DirtyEntities:      len(dirty),
			RemovedEntities:    len(removed),
			Effects:            effectCount,
			DeltaSnapshotBytes: proj.bytes,
			ActiveActions:      a.activeActionCount(),
			ActionEvents:       actionEvents,
			ImpactEvents:       impactEvents,
			HealthEvents:       healthEvents,
			CmdBacklog:         backlog,
			CmdMaxBacklog:      maxBacklog,
			CatchupSteps:       catchup,
			DroppedOps:         a.stats.DroppedOps,
			DroppedTicks:       a.stats.DroppedTicks,
		})
	}
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
		c := *ecs.Get[components.Crafting](a.sim, e)
		recipe, ok := a.recipes[c.RecipeID]
		uid := a.players[e]
		success := false
		if ok && a.sim.IsAlive(e) &&
			(c.Committed || !ecs.Has[components.Dead](a.sim, e)) {
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
		if ecs.Has[components.ActionState](a.sim, e) {
			components.CompleteAction(a.sim, e)
		}
		ecs.Remove[components.Crafting](a.sim, e)
		a.outbox = append(a.outbox, PushEffect{
			Route:   proto.RouteCraftDone,
			Payload: &proto.CraftDone{Uid: uid, RecipeId: c.RecipeID, Success: success},
		})
	}
}

// stampDead 给本 tick 新死亡的实体补盖死亡 tick（系统层不知道世界时钟），
// 并**立刻摘掉它的碰撞体与占格**。
//
// 为什么死亡就要摘碰撞体：NPC 尸体默认还要保留 10 秒（见 NpcCorpseRetentionTicks），
// 期间实体仍然"活着"（只是挂 Dead 标记）。如果不摘碰撞体，死掉的生物会
// 继续挡住玩家——表现为"怪明明死了，走过去还是被卡住"。
// 占格（creatureOccupancy）同理：尸体不该阻止放置建筑。
//
// 注意这里是**幂等**的：因为用 SinceTick==0 判定"首次死亡"，
// 摘除动作只会执行一次。
func (a *WorldActor) stampDead() {
	var fresh []ecs.Entity
	ecs.Query[components.Dead](a.sim, func(e ecs.Entity, d *components.Dead) {
		if d.SinceTick == 0 {
			d.SinceTick = a.tick
			ecs.MarkDirty[components.Dead](a.sim, e)
			fresh = append(fresh, e)
		}
	})
	for _, e := range fresh {
		// 摘掉碰撞形状（Collide.OnRemove 会通知索引注销）。
		// 同时移除 Moveable：尸体不会自己动，索引的"动态层"判据是
		// CanSelfMove（= 有 Moveable），留着会让它继续占着动态层。
		if ecs.Has[components.Collide](a.sim, e) {
			ecs.Remove[components.Collide](a.sim, e)
		}
		if ecs.Has[components.Moveable](a.sim, e) {
			ecs.Remove[components.Moveable](a.sim, e)
		}
	}
	// 动物占格同步（下一 tick 的 CreatureOccupancySystem 也会兜底，
	// 但立刻清一次可以让"死亡当 tick 就能放建筑"）。
	if len(fresh) > 0 && a.creatureTiles != nil {
		a.creatureTiles.Sync(a.sim)
	}
}

// cleanupCorpses 回收尸体实体。
//
// 保留策略（按"谁来回收"分两类，之前写反了）：
//   - **玩家**：永久保留（重连要复用同一个实体，靠 Offline TTL 单独回收，
//     见 cleanupOffline）。玩家尸体不该按 NPC 的时限销毁。
//   - **NPC**：死亡后保留 NPC corpseRetentionTicks（缺省 200 tick ≈ 10 秒），
//     给玩家留一点"看到尸体/拾取"的窗口，然后回收。
//
// 历史 bug：原实现是"玩家跳过、NPC 保留 CorpseRetentionTicks(1200≈60s)"——
// 等于玩家永不回收、NPC 拖 60 秒，正好与需求相反。现在 NPC 的保留时长
// 单独可配（NpcCorpseRetentionTicks），与玩家彻底解耦。
func (a *WorldActor) cleanupCorpses() {
	retention := a.cfg.NpcCorpseRetentionTicks
	if retention <= 0 {
		return
	}
	var expired []ecs.Entity
	ecs.Query[components.Dead](a.sim, func(e ecs.Entity, d *components.Dead) {
		// 玩家尸体不在这里回收（重连复用实体，由 cleanupOffline 的 TTL 负责）。
		if ecs.Has[components.Player](a.sim, e) {
			return
		}
		if d.SinceTick > 0 && a.tick-d.SinceTick >= int64(retention) {
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
		if c.Seq != 0 && !a.replay {
			// 序号锚定和解要求**接受乱序 + 冗余重发**（客户端每 tick 会把未确认的操作再发一遍，
			// 丢包也不会丢输入）：
			//   · 已经**消费**过的（seq <= 对外 ACK）直接丢 —— 重复包不再入队；
			//   · 其余一律交给控制队列，由它按 seq 插入去重（还在排队里的重复包会被忽略）。
			// 旧的"seq 必须大于已收到"的严格门会丢掉晚到的补齐包 —— 那正是丢包后场景永久失配的来源。
			epoch := a.inputAcks[c.UID].Epoch
			if epoch == 0 || c.InputEpoch == 0 || c.InputEpoch != epoch || c.Seq <= a.inputAcks[c.UID].Seq {
				continue
			}
		}
		if !a.cmds.Handle(c) {
			continue
		}
		if c.Seq != 0 && !a.replay {
			// ⚠️ 去重/排序看"已收到"，对外 ACK 看"已消费"，这是**两个**计数。
			// 所有客户端操作（移动/攻击/取消）都统一在**被消费之后**才 ACK —— 见 commitConsumedInputs。
			a.inputReceived[c.UID] = c.Seq
		}
		a.recordJournal(c.Kind, c.UID, c.Seq, c.RequestID, c.Data)
	}
	a.commands = a.commands[:0]
}

// dropOverdueTicks 是 tick 超时保护：世界时钟落后墙钟太多时，**丢掉欠下的 tick**（把时钟追平），
// 而不是让 Tick 在邮箱里无限积压。
//
// 为什么必须丢：定时器按固定间隔发 Tick，处理不过来就会积压 —— 积压会让世界时钟越来越落后，
// 而且永远追不回（每个 Tick 只推进一个间隔），最终变成雪崩（延迟对所有玩家一起涨）。
// 丢 tick 的代价是世界少演化那几步（过载时的"慢动作"），但延迟有界、可观测（DroppedTicks）。
func (a *WorldActor) dropOverdueTicks(now time.Time) {
	if a.tickStartWall.IsZero() || a.cfg.TickInterval <= 0 {
		return
	}
	behind := int64(now.Sub(a.tickStartWall) / a.cfg.TickInterval)
	debt := behind - a.tick
	if debt > maxTickDebt {
		a.tick += debt - 1 // 留一个 tick 给本次正常推进
		a.stats.DroppedTicks += uint64(debt - 1)
	}
}

// commitConsumedInputs 把本 tick 真正**消费掉**的移动意图推进到对外的 ACK。
//
// 语义：ACK = "这条输入已经烘进这份快照里的自己状态"。快照是在本 tick 的移动系统跑完之后生成的，
// 所以必须在这里（系统之后）记，而不是在命令入队时记 —— 否则同一 tick 里被覆盖/丢弃的那条也会被
// 确认，客户端据此裁历史、并认定权威已包含它，其实没有（和解永远对不上，是"偶发卡一下"的来源之一）。
func (a *WorldActor) commitConsumedInputs() {
	q := ecs.Resource[systems.ControlQueue](a.sim)
	if len(q.Consumed) == 0 {
		return
	}
	if a.replay { // 回放（存档/日志重演）不碰在线 ACK
		q.Consumed = q.Consumed[:0]
		return
	}
	for _, c := range q.Consumed {
		// ⚠️ 被**仲裁层**拒绝的操作（目标无效/忙碌等）也要推进 ACK：它确实被处理过了，
		//    客户端得能裁历史、别让 pending 无界增长（拒绝原因通过 outcome 事件回执）。
		//    命令层就没接住的（不是自己的实体等）不会进队列，自然也不会 ACK。
		if c.Seq == 0 {
			continue
		}
		uid, ok := a.players[c.Entity]
		if !ok {
			continue
		}
		ack, ok := a.inputAcks[uid]
		if !ok || c.Seq <= ack.Seq {
			continue
		}
		ack.Seq = c.Seq
		a.inputAcks[uid] = ack
	}
	q.Consumed = q.Consumed[:0]
}

func (a *WorldActor) acceptsInputIdentity(uid string, epoch, seq uint64) bool {
	if epoch == 0 && seq == 0 {
		return true
	}
	ack, ok := a.inputAcks[uid]
	return ok && epoch != 0 && seq != 0 && epoch == ack.Epoch && seq > ack.Seq
}

func (a *WorldActor) cloneInputAcks() map[string]InputAck {
	if len(a.inputAcks) == 0 {
		return nil
	}
	out := make(map[string]InputAck, len(a.inputAcks))
	for uid, ack := range a.inputAcks {
		out[uid] = ack
	}
	return out
}

func (a *WorldActor) activeActionCount() int {
	count := 0
	ecs.Query[components.ActionState](a.sim, func(e ecs.Entity, state *components.ActionState) {
		count++
	})
	return count
}

func (a *WorldActor) drainActionStats() []ActionStat {
	events := components.DrainActionMetrics(a.sim)
	out := make([]ActionStat, 0, len(events))
	for _, event := range events {
		out = append(out, ActionStat{
			Stage:  actionMetricStageName(event.Stage),
			Kind:   actionKindName(event.Kind),
			Reason: actionReasonName(event.Reason),
		})
	}
	return out
}

func actionMetricStageName(stage components.ActionMetricStage) string {
	switch stage {
	case components.ActionMetricStarted:
		return "started"
	case components.ActionMetricCommitted:
		return "committed"
	case components.ActionMetricCompleted:
		return "completed"
	case components.ActionMetricCanceled:
		return "canceled"
	case components.ActionMetricRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

func actionKindName(kind components.ActionKind) string {
	switch kind {
	case components.ActionAttack:
		return "attack"
	case components.ActionChop:
		return "chop"
	case components.ActionMine:
		return "mine"
	case components.ActionPick:
		return "pick"
	case components.ActionCraft:
		return "craft"
	case components.ActionSleep:
		return "sleep"
	default:
		return "unknown"
	}
}

func actionReasonName(reason game.ActionOutcomeReason) string {
	switch reason {
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSPECIFIED:
		return "none"
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_MOVED:
		return "moved"
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DAMAGED:
		return "damaged"
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_DEAD:
		return "dead"
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT:
		return "explicit"
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_BUSY:
		return "busy"
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_TARGET:
		return "invalid_target"
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_UNSUPPORTED:
		return "unsupported"
	case game.ActionOutcomeReason_ACTION_OUTCOME_REASON_INVALID_ACTOR:
		return "invalid_actor"
	default:
		return "unknown"
	}
}

func domainEventStats(events []*game.WorldEvent) ([]ImpactStat, []HealthChangeStat) {
	impacts := make([]ImpactStat, 0)
	healthChanges := make([]HealthChangeStat, 0)
	for _, event := range events {
		if impact := event.GetImpact(); impact != nil {
			result := "unknown"
			switch impact.Result {
			case game.CombatImpactResult_COMBAT_IMPACT_RESULT_HIT:
				result = "hit"
			case game.CombatImpactResult_COMBAT_IMPACT_RESULT_BLOCKED:
				result = "blocked"
			case game.CombatImpactResult_COMBAT_IMPACT_RESULT_IMMUNE:
				result = "immune"
			case game.CombatImpactResult_COMBAT_IMPACT_RESULT_MISS:
				result = "miss"
			}
			impacts = append(impacts, ImpactStat{Result: result})
		}
		if change := event.GetHealthChanged(); change != nil {
			cause := "unknown"
			switch change.Cause {
			case game.HealthChangeCause_HEALTH_CHANGE_CAUSE_ATTACK:
				cause = "attack"
			case game.HealthChangeCause_HEALTH_CHANGE_CAUSE_POISON:
				cause = "poison"
			case game.HealthChangeCause_HEALTH_CHANGE_CAUSE_STARVATION:
				cause = "starvation"
			case game.HealthChangeCause_HEALTH_CHANGE_CAUSE_WEATHER:
				cause = "weather"
			case game.HealthChangeCause_HEALTH_CHANGE_CAUSE_HEALING:
				cause = "healing"
			}
			healthChanges = append(healthChanges, HealthChangeStat{Cause: cause})
		}
	}
	return impacts, healthChanges
}

// recordJournal 记录一条指令日志（重放模式跳过，避免重复记录）。
func (a *WorldActor) recordJournal(kind CommandKind, uid string, seq, requestID uint64, data any) {
	if a.replay {
		return
	}
	e := JournalEntry{Tick: a.tick, UID: uid, Seq: seq, RequestID: requestID, Kind: kind}
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
		components.BeginTickEvents(a.sim, t)
		for _, e := range byTick[t] {
			a.applyEntry(e)
		}
		a.applyCommands()
		a.sim.RunSystems(a.cfg.TickInterval)
		a.cmds.applyActionCommits()
		a.completeCrafts()
		a.processDrops()
		a.stampDead()
		a.cleanupCorpses()
		components.DrainTickEvents(a.sim)
		components.DrainActionMetrics(a.sim)
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
	components.DrainTickEvents(a.sim)
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
			if player, ok := a.findPlayer(e.UID); ok {
				a.commands = append(a.commands, Command{
					UID: e.UID, Seq: e.Seq, RequestID: e.RequestID, Kind: CommandCraft,
					Data: CraftData{Player: player, RecipeID: id},
				})
			}
		}
	case JournalBuild:
		var kind int32
		if json.Unmarshal(e.Data, &kind) == nil {
			a.cmds.build(e.UID, components.BuildingKind(kind))
		}
	case CommandMove, CommandAttack, CommandGather, CommandPickup, CommandUse, CommandEquip, CommandChop, CommandMine, CommandAutomate, CommandDrop, CommandCancelCraft, CommandSplit, CommandPlace, CommandDemolish, CommandCraft, CommandSleep, CommandCancelAction, CommandHaunt:
		if d := e.decodeData(); d != nil {
			a.commands = append(a.commands, Command{
				UID: e.UID, Seq: e.Seq, RequestID: e.RequestID, Kind: e.Kind, Data: d,
			})
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

// processDrops 保留世界 tick/replay 的统一调用点，具体职责由 DropProcessor 承担。
func (a *WorldActor) processDrops() {
	a.drops.Process(a.tick)
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

// QuerySnapshot 请求登录基线快照。UID 非空时只含该玩家视野，并记下兴趣光标。
type QuerySnapshot struct {
	UID string
}

// CreatePlayer 创建玩家实体并返回实体 ID（登录时使用，请求-应答）。
type CreatePlayer struct {
	UID string
}
