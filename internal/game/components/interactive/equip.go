package interactive

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// Equipment 装备实体标记：挂在被装备的物品实体上，记录其物品 kind + 当前耐久。
// 卸下回背包时以这里的耐久为准（Chopper/Miner 是能力副本；两者应保持一致）。
type Equipment struct {
	Kind       components.ItemKind
	Durability int
}

type equipmentCodec struct{}

func (equipmentCodec) Encode(v Equipment) ([]byte, error) {
	return pb.Marshal(&game.ItemStack{Kind: v.Kind, Durability: int32(v.Durability)})
}

func (equipmentCodec) Decode(b []byte) (Equipment, error) {
	var m game.ItemStack
	if err := pb.Unmarshal(b, &m); err != nil {
		return Equipment{}, err
	}
	return Equipment{Kind: components.ItemKind(m.Kind), Durability: int(m.Durability)}, nil
}

// RegisterEquipment 注册 Equipment 组件 codec。
func RegisterEquipment(w *ecs.World) {
	ecs.RegisterComponent(w, "Equipment", equipmentCodec{})
}
