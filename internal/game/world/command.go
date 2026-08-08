package world

import (
	"encoding/json"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// CommandKind 命令类型。
type CommandKind uint8

const (
	CommandMove CommandKind = iota + 1
	CommandAttack
	CommandGather
	CommandPickup
	CommandUse
	CommandEquip
	CommandChop
	CommandMine
	CommandDrop

	// journal 专用事件（复用 CommandKind，仅出现在指令日志里）：
	JournalJoin       CommandKind = 10 // 登录/建号（含重连复用）
	JournalDisconnect CommandKind = 11 // 断线（挂 Offline）
	JournalDestroy    CommandKind = 12 // 离线超时销毁实体
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

// PickupData 拾取命令的数据：拾取者 + 掉落物实体（带 Loot 组件）。
type PickupData struct {
	Player ecs.Entity
	Target ecs.Entity
}

// UseData 使用命令的数据：使用者 + 使用的物品类型。
type UseData struct {
	Player ecs.Entity
	Kind   components.ItemKind
}

// EquipData 装备工具命令的数据：使用者 + 工具 kind（0 = 卸下徒手）。
type EquipData struct {
	Player ecs.Entity
	Kind   components.ItemKind
}

// ChopData 砍伐命令的数据：执行者 + 目标（Workable{CHOP}）。
type ChopData struct {
	Player ecs.Entity
	Target ecs.Entity
}

// MineData 挖掘命令的数据：执行者 + 目标（Workable{MINE}）。
type MineData struct {
	Player ecs.Entity
	Target ecs.Entity
}

// DropData 丢弃命令的数据：丢弃者 + 物品类型 + 数量。
type DropData struct {
	Player ecs.Entity
	Kind   components.ItemKind
	Count  int
}

// JournalEntry 是指令日志里的一条记录：记录"哪个 tick、谁、做了什么"。
// 目的：回放整个世界的确定性模拟（input journal），与存档互验。
//   - Tick：命令被实际应用（applyCommands）或事件发生时所在的世界 tick；
//   - UID：操作者；Seq：操作者侧序号（当前网关未填充，预留去重/校验）；
//   - Kind：CommandMove/Attack/Gather 或 JournalJoin/Disconnect/Destroy；
//   - Data：命令载荷的 JSON（kind 决定解码目标类型）。
type JournalEntry struct {
	Tick int64           `json:"tick"`
	UID  string          `json:"uid"`
	Seq  uint64          `json:"seq"`
	Kind CommandKind     `json:"kind"`
	Data json.RawMessage `json:"data,omitempty"`
}

// decodeData 按 kind 把 JSON 载荷还原为类型化命令数据。
func (e JournalEntry) decodeData() any {
	switch e.Kind {
	case CommandMove:
		var d MoveData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandAttack:
		var d AttackData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandGather:
		var d GatherData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandPickup:
		var d PickupData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandUse:
		var d UseData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandEquip:
		var d EquipData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandChop:
		var d ChopData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandMine:
		var d MineData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandDrop:
		var d DropData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	}
	return nil
}
