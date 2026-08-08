package components

import (
	"sort"

	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Inventory 玩家资源计数（最小形态；完整背包系统在 M5 二期）。
// 确定性纪律：系统按 key 增删，不遍历 map。
type Inventory struct {
	Resources map[ResourceKind]int32
}

type inventoryCodec struct{}

func (inventoryCodec) Encode(v Inventory) ([]byte, error) {
	if v.Resources == nil {
		v.Resources = map[ResourceKind]int32{}
	}
	// repeated 字段按 kind 排序编码，保证确定性（回放/存档一致）。
	kinds := make([]int, 0, len(v.Resources))
	for k := range v.Resources {
		kinds = append(kinds, int(k))
	}
	sort.Ints(kinds)
	counts := make([]*game.ResourceCount, 0, len(kinds))
	for _, k := range kinds {
		kind := game.ResourceKind(k)
		counts = append(counts, &game.ResourceCount{Kind: kind, Count: v.Resources[kind]})
	}
	return pb.Marshal(&game.Inventory{Resources: counts})
}

func (inventoryCodec) Decode(b []byte) (Inventory, error) {
	var inv game.Inventory
	if err := pb.Unmarshal(b, &inv); err != nil {
		return Inventory{}, err
	}
	res := make(map[ResourceKind]int32, len(inv.Resources))
	for _, rc := range inv.Resources {
		res[rc.Kind] = rc.Count
	}
	return Inventory{Resources: res}, nil
}
