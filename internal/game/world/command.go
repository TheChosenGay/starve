package world

import "starve/internal/ecs"

// CommandKind 命令类型。
type CommandKind uint8

const (
	CommandMove CommandKind = iota + 1
	CommandAttack
	CommandGather
)

// Command 玩家意图的统一包装：带来源与序号，便于校验/审计/顺序保证。
// WorldActor 收到后只入命令缓冲，tick 时统一消费（到达速率与模拟速率解耦）。
type Command struct {
	UID  int64
	Seq  uint64
	Kind CommandKind
	Data any
}

// MoveData 移动命令的数据：目标实体 + 位移。
type MoveData struct {
	Entity ecs.Entity
	DX, DY int
}
