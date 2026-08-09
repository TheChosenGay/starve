package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Equipped 玩家当前手持的工具（Kind=0 表示徒手）。
// 工具属性不复制到玩家身上：动作时动态从模板解析（物品状态如耐久留在背包实例）。
type Equipped struct {
	Kind ItemKind
}

type equippedCodec struct{}

func (equippedCodec) Encode(v Equipped) ([]byte, error) {
	return pb.Marshal(&game.Equipped{Kind: v.Kind})
}

func (equippedCodec) Decode(b []byte) (Equipped, error) {
	var e game.Equipped
	if err := pb.Unmarshal(b, &e); err != nil {
		return Equipped{}, err
	}
	return Equipped{Kind: e.Kind}, nil
}
