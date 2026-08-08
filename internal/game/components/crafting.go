package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Crafting 制作中的状态（挂在玩家身上）：配方 + 剩余 tick。
// 由 CraftSystem 每 tick 倒计时，到点世界产出并移除。
type Crafting struct {
	RecipeID  string
	TicksLeft int
}

type craftingCodec struct{}

func (craftingCodec) Encode(v Crafting) ([]byte, error) {
	return pb.Marshal(&game.Crafting{RecipeId: v.RecipeID, TicksLeft: int64(v.TicksLeft)})
}

func (craftingCodec) Decode(b []byte) (Crafting, error) {
	var c game.Crafting
	if err := pb.Unmarshal(b, &c); err != nil {
		return Crafting{}, err
	}
	return Crafting{RecipeID: c.RecipeId, TicksLeft: int(c.TicksLeft)}, nil
}
