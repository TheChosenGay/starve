package world

import (
	"github.com/TheChosenGay/actor"
)

// RoomInfo 是 NodeManager 持有的一个房间/世界的信息。
type RoomInfo struct {
	RoomName    string
	AccessToken string
	PID         *actor.PID
}

// RoomMeta 是创建世界时给「房间工厂」的元信息（含按房间名落盘的 saveRoot）。
type RoomMeta struct {
	RoomName string
	SaveRoot string
}

// CreateWorld 由大厅发到世界节点的 NodeManager：创建（子化）一个 WorldActor。
type CreateWorld struct {
	RoomName    string
	AccessToken string
	Config      RoomConfig // 可选，如最大容纳人数
}

// RoomConfig 房间（世界）的可选配置。
type RoomConfig struct {
	MaxPlayers int
}

// CreateWorldResp 创建结果：返回新世界 actor 的 PID。
type CreateWorldResp struct {
	RoomName string
	PID      *actor.PID
}

// QueryToken 由 Business（combet）在握手时携带 accessToken 查询归属世界。
type QueryToken struct{ AccessToken string }

// QueryTokenResp 返回 accessToken 对应的世界；无效时 OK=false。
type QueryTokenResp struct {
	RoomName string
	PID      *actor.PID
	OK       bool
}

// QueryWorlds 查询当前节点有多少个世界（供大厅/运维）。
type QueryWorlds struct{}

// QueryWorldsResp 返回世界列表。
type QueryWorldsResp struct {
	Rooms []RoomInfo
}

// DestroyWorld 由大厅/运维在房间清空后销毁世界。
type DestroyWorld struct{ RoomName string }

// Shutdown 通知 WorldActor 停止（销毁房间/关服用）。WorldActor 收到后结束 tick。
type Shutdown struct{}

// NodeManager 是世界服务器节点上的房间管理器 actor：维护 accessToken→世界、子化 WorldActor。
type NodeManager struct {
	makeRoom func(meta RoomMeta) actor.IActor // 每个房间 actor 的工厂（世界节点用 WorldActor）
	saveRoot string
	rooms    map[string]*RoomInfo
	tokens   map[string]string // accessToken → roomName
}

// NewNodeManager 创建 NodeManager。makeRoom 用于为每个房间创建房间 actor（带房间名/落盘信息）。
func NewNodeManager(makeRoom func(meta RoomMeta) actor.IActor, saveRoot string) *NodeManager {
	return &NodeManager{
		makeRoom: makeRoom,
		saveRoot: saveRoot,
		rooms:    make(map[string]*RoomInfo),
		tokens:   make(map[string]string),
	}
}

// Producer 返回 NodeManager 的 actor 工厂。
func (m *NodeManager) Producer() actor.Producer {
	return func() actor.IActor { return m }
}

// Receive 处理创建/查询/销毁。
func (m *NodeManager) Receive(ctx actor.IActorContext) {
	switch msg := ctx.Message().(type) {
	case CreateWorld:
		meta := RoomMeta{RoomName: msg.RoomName, SaveRoot: m.saveRoot}
		pid := ctx.SpawnChild(func() actor.IActor { return m.makeRoom(meta) }, msg.RoomName)
		// 启动房间 tick（WorldActor 收到 Start 才自驱动）。
		ctx.Send(pid, Start{})
		m.rooms[msg.RoomName] = &RoomInfo{RoomName: msg.RoomName, AccessToken: msg.AccessToken, PID: pid}
		if msg.AccessToken != "" {
			m.tokens[msg.AccessToken] = msg.RoomName
		}
		ctx.Respond(CreateWorldResp{RoomName: msg.RoomName, PID: pid})

	case QueryToken:
		if roomName, ok := m.tokens[msg.AccessToken]; ok {
			if info, ok := m.rooms[roomName]; ok {
				ctx.Respond(QueryTokenResp{RoomName: roomName, PID: info.PID, OK: true})
				break
			}
		}
		ctx.Respond(QueryTokenResp{OK: false})

	case QueryWorlds:
		rooms := make([]RoomInfo, 0, len(m.rooms))
		for _, info := range m.rooms {
			rooms = append(rooms, *info)
		}
		ctx.Respond(QueryWorldsResp{Rooms: rooms})

	case DestroyWorld:
		if info, ok := m.rooms[msg.RoomName]; ok {
			ctx.Send(info.PID, Shutdown{})
			delete(m.rooms, msg.RoomName)
			delete(m.tokens, info.AccessToken)
		}
	}
}
