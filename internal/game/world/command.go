package world

import "starve/internal/ecs"

// CommandKind 命令类型。
type CommandKind uint8

const (
	CommandMove CommandKind = iota + 1
	CommandAttack
	CommandGather
)

// Command 玩家意图的统一包装：
//   - UID：发起命令的用户 ID（玩家账号，字符串，如 UUID）；
//   - Seq：该用户操作的递增序号——用于去重（网络重传/重复点击）与顺序校验（旧命令丢弃）；
//   - Kind：操作类型（Move / Attack / Gather...）；
//   - Data：操作载荷（具体语义数据，如 MoveData）。注意不含时间戳：
//     重放确定性来自"命令顺序 + 初始存档 + 种子化 RNG"，而不是命令里的墙钟时间。
//
// 节点内它是类型化 Go 结构；跨网络（客户端→网关→世界）时用 protobuf 编码，
// 由 M4 网关按路由注册表反序列化成这个类型。
// WorldActor 收到后只入命令缓冲，tick 时统一消费（到达速率与模拟速率解耦）。
type Command struct {
	UID  string
	Seq  uint64
	Kind CommandKind
	Data any
}

// MoveData 移动命令的数据：目标实体 + 位移。
// Entity 是命令作用的实体 ID——玩家自己的实体由服务器在进世界时分配并告知
// （M4），客户端不能自选；服务器执行时应校验 UID 拥有该实体。
type MoveData struct {
	Entity ecs.Entity
	DX, DY int
}

// AttackData 攻击命令的数据：攻击者 + 目标实体。
type AttackData struct {
	Attacker ecs.Entity
	Target   ecs.Entity
}

// GatherData 采集命令的数据：采集者（玩家实体）+ 目标实体。
type GatherData struct {
	Player ecs.Entity
	Target ecs.Entity
}
