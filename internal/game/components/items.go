package components

import (
	"sort"

	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// ItemStack 物品堆叠：类型 + 数量 + 堆叠上限（0 = 用资源模板默认）+ 耐久（工具用）。
type ItemStack struct {
	Kind       ItemKind
	Count      int
	MaxStack   int
	Durability int
}

// Inventory 玩家背包：物品堆叠（M5 二期最小形态；后续加容量/槽位）。
// 确定性纪律：按 key 增删，不遍历 map 决定顺序；快照编码按 kind 排序。
type Inventory struct {
	Items map[ItemKind]ItemStack
}

// Add 添加物品（堆叠上限/初始耐久由调用方按模板传入；上限 0 = 不限）。
// 返回实际添加数量（超上限截断）。
func (inv *Inventory) Add(kind ItemKind, count, maxStack, durability int) int {
	if inv.Items == nil {
		inv.Items = map[ItemKind]ItemStack{}
	}
	cur := inv.Items[kind]
	if cur.Count == 0 {
		cur = ItemStack{Kind: kind, MaxStack: maxStack, Durability: durability}
	}
	added := count
	if cur.MaxStack > 0 {
		space := cur.MaxStack - cur.Count
		if space <= 0 {
			return 0
		}
		if added > space {
			added = space
		}
	}
	cur.Count += added
	inv.Items[kind] = cur
	return added
}

// Take 消耗物品；数量不足返回 false。
func (inv *Inventory) Take(kind ItemKind, count int) bool {
	if inv.Items == nil {
		return false
	}
	cur, ok := inv.Items[kind]
	if !ok || cur.Count < count {
		return false
	}
	cur.Count -= count
	if cur.Count <= 0 {
		delete(inv.Items, kind)
	} else {
		inv.Items[kind] = cur
	}
	return true
}

// Stack 返回某物品堆（不存在返回零值）。
func (inv *Inventory) Stack(kind ItemKind) ItemStack {
	if inv.Items == nil {
		return ItemStack{}
	}
	return inv.Items[kind]
}

// Degrade 扣工具耐久一次；损坏（耐久归零）时移除该物品并返回 true。
func (inv *Inventory) Degrade(kind ItemKind) bool {
	if inv.Items == nil {
		return false
	}
	cur, ok := inv.Items[kind]
	if !ok || cur.Count <= 0 {
		return false
	}
	cur.Durability--
	if cur.Durability <= 0 {
		delete(inv.Items, kind)
		return true
	}
	inv.Items[kind] = cur
	return false
}

type inventoryCodec struct{}

func (inventoryCodec) Encode(v Inventory) ([]byte, error) {
	if v.Items == nil {
		v.Items = map[ItemKind]ItemStack{}
	}
	return pb.Marshal(&game.Inventory{Items: stacksToProto(v.Items)})
}

func (inventoryCodec) Decode(b []byte) (Inventory, error) {
	var inv game.Inventory
	if err := pb.Unmarshal(b, &inv); err != nil {
		return Inventory{}, err
	}
	items := make(map[ItemKind]ItemStack, len(inv.Items))
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
	m := make(map[ItemKind]ItemStack, len(l.Items))
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
func stacksToProto(items map[ItemKind]ItemStack) []*game.ItemStack {
	out := make([]*game.ItemStack, 0, len(items))
	for _, k := range sortedKinds(items) {
		s := items[k]
		out = append(out, &game.ItemStack{Kind: k, Count: int32(s.Count), MaxStack: int32(s.MaxStack), Durability: int32(s.Durability)})
	}
	return out
}

func sortedKinds[T any](m map[ItemKind]T) []ItemKind {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	out := make([]ItemKind, 0, len(keys))
	for _, k := range keys {
		out = append(out, ItemKind(k))
	}
	return out
}

func listToMap(items []ItemStack) map[ItemKind]ItemStack {
	m := make(map[ItemKind]ItemStack, len(items))
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
