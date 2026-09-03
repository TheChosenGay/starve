package world

import (
	"os"
	"path/filepath"

	"github.com/TheChosenGay/actor"
)

// RoomSavePath 返回某房间的存档路径：<saveRoot>/<RoomName>.bin。
func RoomSavePath(saveRoot, roomName string) string {
	return filepath.Join(saveRoot, roomName+".bin")
}

// NewWorldNode 返回世界服务器节点的房间管理器工厂：每个房间 = 一个 WorldActor，
// 存档一律按房间名存放为 <saveRoot>/<RoomName>.bin，并在创建时自动加载已存存档。
func NewWorldNode(cfg WorldConfig, gc *GameConfig, saveRoot string) actor.Producer {
	makeRoom := func(meta RoomMeta) actor.IActor {
		wa := NewWorldActorWithConfig(cfg, gc)
		path := RoomSavePath(saveRoot, meta.RoomName)
		if path != "" {
			wa.SetSaveSink(func(data []byte) error {
				if err := os.MkdirAll(saveRoot, 0o755); err != nil {
					return err
				}
				return os.WriteFile(path, data, 0o644)
			})
			if data, err := os.ReadFile(path); err == nil {
				_ = wa.Load(data)
			}
		}
		return wa
	}
	return func() actor.IActor { return NewNodeManager(makeRoom, saveRoot) }
}
