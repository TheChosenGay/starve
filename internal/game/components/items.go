package components

import (
	"sort"

	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// ItemStack 物品堆叠：类型 + 数量 + 堆叠上限（0 = 用资源模板默认）+ 耐久（工具用）。
type ItemStack struct {
	Kind       ResourceKind
	Count      int
	MaxStack   int
	Durability int
}

// Inventory 玩家背包：物品堆叠（M5 二期最小形态；后续加容量/槽位）。
// 确定性纪律：按 key 增删，不遍历 map 决定顺序；快照编码按 kind 排序。
type Inventory struct {
	Items map[ResourceKind]ItemStack
}

type inventoryCodec struct{}

func (inventoryCodec) Encode(v Inventory) ([]byte, error) {
	if v.Items == nil {
		v.Items = map[ResourceKind]ItemStack{}
	}
	return pb.Marshal(&game.Inventory{Items: stacksToProto(v.Items)})
}

func (inventoryCodec) Decode(b []byte) (Inventory, error) {
	var inv game.Inventory
	if err := pb.Unmarshal(b, &inv); err != nil {
		return Inventory{}, err
	}
	items := make(map[ResourceKind]ItemStack, len(inv.Items))
	for _, s := range inv.Items {
		items[s.Kind] = ItemStack{Kind: s.Kind, Count: int(s.Count), MaxStack: int(s.MaxStack), Durability: int(s.Durability)}
	}
	return Inventory{Items: items}, nil
}

// Loot 掉落物（死亡/砍伐实体就地转化）：捡走即消失。
type Loot struct {
	Items []ItemStack
}

type lootCodec struct{}

func (lootCodec) Encode(v Loot) ([]byte, error) {
	return pb.Marshal(&game.Loot{Items: stacksToProto(listToMap(v.Items))})
}

func (lootCodec) Decode(b []byte) (Loot, error) {
	var l game.Loot
	if err := pb.Unmarshal(b, &l); err != nil {
		return Loot{}, err
	}
	m := make(map[ResourceKind]ItemStack, len(l.Items))
	for _, s := range l.Items {
		m[s.Kind] = ItemStack{Kind: s.Kind, Count: int(s.Count), MaxStack: int(s.MaxStack), Durability: int(s.Durability)}
	}
	items := make([]ItemStack, 0, len(m))
	for _, k := range sortedKinds(m) {
		items = append(items, m[k])
	}
	return Loot{Items: items}, nil
}

// stacksToProto 把 map 按 kind 排序编码（确定性）。
func stacksToProto(items map[ResourceKind]ItemStack) []*game.ItemStack {
	out := make([]*game.ItemStack, 0, len(items))
	for _, k := range sortedKinds(items) {
		s := items[k]
		out = append(out, &game.ItemStack{Kind: k, Count: int32(s.Count), MaxStack: int32(s.MaxStack), Durability: int32(s.Durability)})
	}
	return out
}

func sortedKinds[T any](m map[ResourceKind]T) []ResourceKind {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	out := make([]ResourceKind, 0, len(keys))
	for _, k := range keys {
		out = append(out, ResourceKind(k))
	}
	return out
}

func listToMap(items []ItemStack) map[ResourceKind]ItemStack {
	m := make(map[ResourceKind]ItemStack, len(items))
	for _, s := range items {
		cur := m[s.Kind]
		cur.Kind = s.Kind
		cur.Count += s.Count
		if cur.MaxStack == 0 {
			cur.MaxStack = s.MaxStack
		}
		m[s.Kind] = cur
	}
	return m
}
