package interactive

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// Equipment 装备实体标记：挂在被装备的物品实体上，记录其物品 kind（卸下时还原背包）。
// 工具能力（Chopper/Miner）、护甲防御（Defense）由各自组件承担，本组件只解决"这实体是什么物品"。
type Equipment struct {
	Kind components.ItemKind
}

type equipmentCodec struct{}

func (equipmentCodec) Encode(v Equipment) ([]byte, error) {
	return pb.Marshal(&game.ItemStack{Kind: v.Kind})
}

func (equipmentCodec) Decode(b []byte) (Equipment, error) {
	var m game.ItemStack
	if err := pb.Unmarshal(b, &m); err != nil {
		return Equipment{}, err
	}
	return Equipment{Kind: components.ItemKind(m.Kind)}, nil
}

// RegisterEquipment 注册 Equipment 组件 codec。
func RegisterEquipment(w *ecs.World) {
	ecs.RegisterComponent(w, "Equipment", equipmentCodec{})
}
