package world

import (
	"time"

	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
)

// WorldActor 是 Actor ↔ ECS 的接缝：一个世界 = 一个 WorldActor + 一个 ecs.World。
//
// 纪律（设计文档 §5.2）：
//   - 命令只入缓冲，tick 统一消费（消息到达速率与模拟速率解耦）
//   - ECS 系统是纯函数，副作用只经 outbox，tick 结束统一 drain
//   - 时间由本 actor 注入固定 dt，ECS 不读 wall clock
//   - tick 内禁止 Request(...).Wait()（同步跨 actor 调用）
type WorldActor struct {
	sim      *ecs.World
	cfg      WorldConfig
	commands []Command
	outbox   []Effect
	tick     int64 // 世界时钟 = tick × dt
	started  bool  // 已启动自驱动 tick（防重复 Start）
}

// NewWorldActor 创建世界 actor。TickInterval 为零值时用 100ms（10Hz）。
func NewWorldActor(cfg WorldConfig) *WorldActor {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 100 * time.Millisecond
	}
	return &WorldActor{
		sim: ecs.NewWorld(),
		cfg: cfg,
	}
}

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
	case CreatePlayer:
		// MVP：登录时在 tick 外直接创建（结构变更走命令缓冲的纪律在 M5 收拢）
		e := a.sim.CreateEntity()
		ecs.Add(a.sim, e, components.Position{X: 0, Y: 0})
		ctx.Respond(e)
	}
}

// onTick 三阶段：命令 → 系统 → outbox。
func (a *WorldActor) onTick(ctx actor.IActorContext) {
	a.applyCommands()
	a.sim.RunSystems(a.cfg.TickInterval)
	a.flushOutbox(ctx)
	a.tick++
}

// applyCommands 校验并执行缓冲中的命令（游戏语义 → ECS 操作）。
func (a *WorldActor) applyCommands() {
	for _, c := range a.commands {
		switch c.Kind {
		case CommandMove:
			a.applyMove(c)
		}
	}
	a.commands = a.commands[:0]
}

func (a *WorldActor) applyMove(c Command) {
	m, ok := c.Data.(MoveData)
	if !ok {
		return // 非法命令：丢弃
	}
	if !ecs.Has[components.Position](a.sim, m.Entity) {
		return // 实体没有位置：丢弃
	}
	p := ecs.Get[components.Position](a.sim, m.Entity)
	ecs.Set(a.sim, m.Entity, components.Position{X: p.X + m.DX, Y: p.Y + m.DY})
}

// flushOutbox 统一执行副作用（发送/推送/存档）。
func (a *WorldActor) flushOutbox(ctx actor.IActorContext) {
	for _, ef := range a.outbox {
		switch e := ef.(type) {
		case PushEffect:
			ctx.Send(&e.To, e.Payload)
		case SendMessageEffect:
			ctx.Send(&e.To, e.Msg)
		case SaveEffect:
			// TODO(M5): 接存档系统
		}
	}
	a.outbox = a.outbox[:0]
}

// QueryPosition 查询实体位置（请求-应答，供外部/网关/测试使用）。
type QueryPosition struct {
	Entity ecs.Entity
}

// CreatePlayer 创建玩家实体并返回实体 ID（登录时使用，请求-应答）。
type CreatePlayer struct {
	UID string
}
