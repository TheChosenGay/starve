package world

import (
	"sync"
	"sync/atomic"
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
	sim      *ecs.World
	cfg      WorldConfig
	commands []Command
	outbox   []Effect
	tick     atomic.Int64          // 世界时钟 = tick × dt（原子，可被外部读）
	started  bool                  // 已启动自驱动 tick（防重复 Start）
	players  map[ecs.Entity]string // 实体 → UID（命令所有权校验）
	pushSink func(PushEffect)      // 推送出口（网关注入）；nil 时 PushEffect 丢弃
	saveSink func([]byte)          // 存档落盘出口（宿主导入，事件触发用）
	saveMu   sync.RWMutex          // 快照锁：tick 持读锁，Save() 持写锁
}

// NewWorldActor 创建世界 actor。
func NewWorldActor(cfg WorldConfig) *WorldActor {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 100 * time.Millisecond
	}
	if cfg.HungerRate <= 0 {
		cfg.HungerRate = 1
	}
	if cfg.GrowthTicks <= 0 {
		cfg.GrowthTicks = 20
	}
	if cfg.AttackDamage <= 0 {
		cfg.AttackDamage = 10
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
		HungerDefaultRate: cfg.HungerRate,
		GrowthTicks:       cfg.GrowthTicks,
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
	return time.Duration(a.tick.Load()) * a.cfg.TickInterval
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
	}
}

// createPlayer 创建玩家实体（位置 + 血量 + 饥饿），登记所有权。
func (a *WorldActor) createPlayer(uid string) ecs.Entity {
	e := a.sim.CreateEntity()
	ecs.Add(a.sim, e, components.Position{X: 0, Y: 0})
	ecs.Add(a.sim, e, components.Health{Cur: 100, Max: 100})
	ecs.Add(a.sim, e, components.Hunger{Level: 100, Rate: a.cfg.HungerRate})
	ecs.Add(a.sim, e, components.Player{UID: uid})
	a.players[e] = uid
	return e
}

// onTick：命令 → 系统 → 快照 → outbox。
func (a *WorldActor) onTick(ctx actor.IActorContext) {
	// 快照锁：模拟段持读锁，保证外部 Save() 能拿到一致快照
	a.saveMu.RLock()
	a.applyCommands()
	a.sim.RunSystems(a.cfg.TickInterval)
	removed := a.drainRemoved()
	dirty := a.sim.DrainDirtySorted()
	delta := DeltaSnapshot(a.sim, dirty, removed)
	a.tick.Add(1)
	a.saveMu.RUnlock()

	// 每 tick 广播增量快照（含昼夜等世界状态）
	a.outbox = append(a.outbox, PushEffect{
		Route:   proto.RouteSnapshotDelta,
		Payload: delta,
	})
	a.flushOutbox(ctx)
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
		}
	}
	a.commands = a.commands[:0]
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
