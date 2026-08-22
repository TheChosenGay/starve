package world

import (
	"encoding/json"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/pkg/proto"
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
	CommandAutomate
	CommandDrop
	CommandCancelCraft
	CommandSplit
	CommandPlace
	CommandDemolish
	CommandCraft
	CommandSleep
	CommandCancelAction
	CommandHaunt

	// journal 专用事件（复用 CommandKind，仅出现在指令日志里）：
	JournalJoin       CommandKind = 20 // 登录/建号（含重连复用）
	JournalDisconnect CommandKind = 21 // 断线（挂 Offline）
	JournalDestroy    CommandKind = 22 // 离线超时销毁实体
	JournalCraft      CommandKind = 23 // 制作开始（recipe_id 在 Data）
	JournalBuild      CommandKind = 24 // 建造创建（kind 在 Data，JSON 编码的 BuildingKind）
)

// Command 玩家意图的统一包装：
//   - UID：发起命令的用户 ID（玩家账号，字符串，如 UUID）；
//   - InputEpoch：登录输入世代；重连后变化，防止旧连接迟到输入污染新连接；
//   - Seq：该用户操作的递增序号——用于去重（网络重传/重复点击）与顺序校验（旧命令丢弃）；
//   - Kind：操作类型（Move / Attack / Gather...）；
//   - Data：操作载荷（具体语义数据，如 MoveData）。注意不含时间戳：
//     重放确定性来自"命令顺序 + 初始存档 + 种子化 RNG"，而不是命令里的墙钟时间。
//
// 节点内它是类型化 Go 结构；跨网络（客户端→网关→世界）时用 protobuf 编码，
// 由 M4 网关按路由注册表反序列化成这个类型。
// WorldActor 收到后只入命令缓冲，tick 时统一消费（到达速率与模拟速率解耦）。
type Command struct {
	UID        string
	InputEpoch uint64
	Seq        uint64
	RequestID  uint64
	Kind       CommandKind
	Data       any
}

// MoveData 移动命令的数据：目标实体 + 方向步进（-1/0/1；0,0=停止）。
// 命令进 Moveable.Queue 缓存（顺序应用），MoveSystem 每 tick 按步进间隔消费；
// 位置不由命令直接修改。客户端长按节流发送，停止时发 0,0 清空队列。
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

// EquipData 装备命令：Kind 非 0 装备该物品；Kind=0 卸下（Slot=0 卸全部，否则只卸该槽）。
type EquipData struct {
	Player ecs.Entity
	Kind   components.ItemKind
	Slot   components.Slot
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

// AutomateData 自动行为命令的数据：ANY 兼容空格，ATTACK_ONLY 对应 F 键；
// 客户端不传目标，由服务端在 AOI 内权威选择。
type AutomateData struct {
	Player ecs.Entity
	Mode   proto.AutomateMode
}

// DropData 丢弃命令的数据：丢弃者 + 物品类型 + 数量。
type DropData struct {
	Player ecs.Entity
	Kind   components.ItemKind
	Count  int
}

// CancelCraftData 取消制作命令的数据：取消者。
type CancelCraftData struct {
	Player ecs.Entity
}

// CraftData 制作命令的数据：制作者 + 配方 ID。
type CraftData struct {
	Player   ecs.Entity
	RecipeID string
}

// SleepData 睡眠命令只携带玩家；目标营火由服务端权威选择。
type SleepData struct {
	Player ecs.Entity
}

// HauntData 作祟命令携带死亡玩家与客户端选定的复活雕像。
type HauntData struct {
	Player ecs.Entity
	Target ecs.Entity
}

// CancelActionData 显式取消玩家当前持续动作。
type CancelActionData struct {
	Player ecs.Entity
}

// SplitData 拆分命令的数据：拆分者 + 源槽位 + 数量（放入第一个空槽）。
type SplitData struct {
	Player   ecs.Entity
	FromSlot int
	Count    int
}

// PlaceData 放置指令：执行者 + 目标建筑实体 + 坐标（左上角锚点）。
type PlaceData struct {
	Actor  ecs.Entity
	Entity ecs.Entity
	X, Y   int
}

// DemolishData 拆除指令：执行者 + 目标建筑实体。
type DemolishData struct {
	Actor  ecs.Entity
	Target ecs.Entity
}

// JournalEntry 是指令日志里的一条记录：记录"哪个 tick、谁、做了什么"。
// 目的：回放整个世界的确定性模拟（input journal），与存档互验。
//   - Tick：命令被实际应用（applyCommands）或事件发生时所在的世界 tick；
//   - UID：操作者；Seq：操作者侧序号（移动命令由网关从 PlayerMove.seq 填充）；
//   - Kind：CommandMove/Attack/Gather 或 JournalJoin/Disconnect/Destroy；
//   - Data：命令载荷的 JSON（kind 决定解码目标类型）。
type JournalEntry struct {
	Tick      int64           `json:"tick"`
	UID       string          `json:"uid"`
	Seq       uint64          `json:"seq"`
	RequestID uint64          `json:"request_id,omitempty"`
	Kind      CommandKind     `json:"kind"`
	Data      json.RawMessage `json:"data,omitempty"`
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
	case CommandAutomate:
		var d AutomateData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandDrop:
		var d DropData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandCancelCraft:
		var d CancelCraftData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandCraft:
		var d CraftData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandSleep:
		var d SleepData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandHaunt:
		var d HauntData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandCancelAction:
		var d CancelActionData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandSplit:
		var d SplitData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandPlace:
		var d PlaceData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	case CommandDemolish:
		var d DemolishData
		if json.Unmarshal(e.Data, &d) == nil {
			return d
		}
	}
	return nil
}
