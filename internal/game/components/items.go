package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// ItemStack 物品堆叠：类型 + 数量 + 堆叠上限（0 = 用资源模板默认）+ 耐久（工具用）。
type ItemStack struct {
	Kind       ItemKind
	Count      int
	MaxStack   int
	Durability int
}

// Inventory 玩家背包：槽位数组（固定容量，空槽 = 零值 ItemStack）。
// 容量由世界配置（WorldConfig.InventorySlots）在创建/加载时确定；
// 组件方法只做数据操作，不负责 dirty 标记（调用方 MarkDirty）。
type Inventory struct {
	Slots []ItemStack
}

// CountOf 某种物品的总数量（跨槽求和）。
func (inv *Inventory) CountOf(kind ItemKind) int {
	n := 0
	for _, s := range inv.Slots {
		if s.Kind == kind {
			n += s.Count
		}
	}
	return n
}

// Add 添加物品：先叠进同 kind 有空间的槽，再放空槽；返回实际添加数。
func (inv *Inventory) Add(kind ItemKind, count, maxStack, durability int) int {
	added := 0
	for i := range inv.Slots {
		if added >= count {
			break
		}
		s := &inv.Slots[i]
		if s.Kind != kind || s.Count <= 0 {
			continue
		}
		space := count - added
		if s.MaxStack > 0 {
			free := s.MaxStack - s.Count
			if free <= 0 {
				continue
			}
			if space > free {
				space = free
			}
		}
		s.Count += space
		added += space
	}
	for i := range inv.Slots {
		if added >= count {
			break
		}
		s := &inv.Slots[i]
		if s.Kind != 0 && s.Count > 0 {
			continue
		}
		space := count - added
		stack := ItemStack{Kind: kind, MaxStack: maxStack, Durability: durability}
		if stack.MaxStack > 0 && space > stack.MaxStack {
			space = stack.MaxStack
		}
		stack.Count = space
		inv.Slots[i] = stack
		added += space
	}
	return added
}

// SpaceFor 某种物品还能放下的数量（跨槽 + 空槽容量；maxStack<=0 视为不限）。
func (inv *Inventory) SpaceFor(kind ItemKind, maxStack int) int {
	if maxStack <= 0 {
		return 1 << 30
	}
	free := 0
	empties := 0
	for _, s := range inv.Slots {
		if s.Kind == kind && s.Count > 0 {
			free += maxStack - s.Count
		} else if s.Kind == 0 || s.Count <= 0 {
			empties++
		}
	}
	return free + empties*maxStack
}

// Take 消耗 count 个物品（跨槽取）；数量不足时不改动并返回 false（原子）。
func (inv *Inventory) Take(kind ItemKind, count int) bool {
	if inv.CountOf(kind) < count {
		return false
	}
	need := count
	for i := range inv.Slots {
		if need <= 0 {
			break
		}
		s := &inv.Slots[i]
		if s.Kind != kind || s.Count <= 0 {
			continue
		}
		if s.Count > need {
			s.Count -= need
			need = 0
		} else {
			need -= s.Count
			inv.Slots[i] = ItemStack{}
		}
	}
	return true
}

// Slot 返回某槽的物品（越界返回零值）。
func (inv *Inventory) Slot(i int) ItemStack {
	if inv.Slots == nil || i < 0 || i >= len(inv.Slots) {
		return ItemStack{}
	}
	return inv.Slots[i]
}

// SetSlot 设置某槽（越界忽略）。
func (inv *Inventory) SetSlot(i int, s ItemStack) {
	if inv.Slots != nil && i >= 0 && i < len(inv.Slots) {
		inv.Slots[i] = s
	}
}

// FirstEmptySlot 返回第一个空槽（无则 -1）。
func (inv *Inventory) FirstEmptySlot() int {
	for i, s := range inv.Slots {
		if s.Kind == 0 || s.Count <= 0 {
			return i
		}
	}
	return -1
}

// NonEmptyCount 非空槽数量。
func (inv *Inventory) NonEmptyCount() int {
	n := 0
	for _, s := range inv.Slots {
		if s.Kind != 0 && s.Count > 0 {
			n++
		}
	}
	return n
}

// Degrade 扣工具耐久一次；损坏（耐久归零）时移除该槽物品并返回 true。
func (inv *Inventory) Degrade(kind ItemKind) bool {
	if inv.Slots == nil {
		return false
	}
	for i := range inv.Slots {
		s := &inv.Slots[i]
		if s.Kind != kind || s.Count <= 0 {
			continue
		}
		s.Durability--
		if s.Durability <= 0 {
			inv.Slots[i] = ItemStack{}
			return true
		}
		inv.Slots[i] = *s
		return false
	}
	return false
}

type inventoryCodec struct{}

func (inventoryCodec) Encode(v Inventory) ([]byte, error) {
	return pb.Marshal(&game.Inventory{Items: slotsToProto(v.Slots)})
}

func (inventoryCodec) Decode(b []byte) (Inventory, error) {
	var inv game.Inventory
	if err := pb.Unmarshal(b, &inv); err != nil {
		return Inventory{}, err
	}
	slots := make([]ItemStack, 0, len(inv.Items))
	for _, s := range inv.Items {
		slots = append(slots, ItemStack{Kind: s.Kind, Count: int(s.Count), MaxStack: int(s.MaxStack), Durability: int(s.Durability)})
	}
	return Inventory{Slots: slots}, nil
}

// slotsToProto 按顺序编码（含空槽；空 ItemStack 编码为空字节，保持槽位）。
func slotsToProto(slots []ItemStack) []*game.ItemStack {
	out := make([]*game.ItemStack, 0, len(slots))
	for _, s := range slots {
		out = append(out, &game.ItemStack{Kind: s.Kind, Count: int32(s.Count), MaxStack: int32(s.MaxStack), Durability: int32(s.Durability)})
	}
	return out
}

// Lootable 受激拾取能力（-able）：掉落物实体携带，捡走即消失。
// 实现 interactive.Actived（Usable：还有东西可捡）；物品入包由命令层完成（堆叠上限来自模板）。
type Lootable struct {
	Items []ItemStack
}

type lootableCodec struct{}

func (lootableCodec) Encode(v Lootable) ([]byte, error) {
	return pb.Marshal(&game.Loot{Items: slotsToProto(v.Items)})
}

func (lootableCodec) Decode(b []byte) (Lootable, error) {
	var l game.Loot
	if err := pb.Unmarshal(b, &l); err != nil {
		return Lootable{}, err
	}
	items := make([]ItemStack, 0, len(l.Items))
	for _, s := range l.Items {
		items = append(items, ItemStack{Kind: s.Kind, Count: int(s.Count), MaxStack: int(s.MaxStack), Durability: int(s.Durability)})
	}
	return Lootable{Items: items}, nil
}

// Usable 还有可拾取的物品。
func (l Lootable) Usable(_ *ecs.World, _ ecs.Entity) bool { return len(l.Items) > 0 }

// Loot 遗留掉落物组件（已退役）：仅用于旧存档加载 + migrateLoot 迁移，新代码不再创建。
type Loot struct {
	Items []ItemStack
}

type lootCodec struct{}

func (lootCodec) Encode(v Loot) ([]byte, error) {
	return pb.Marshal(&game.Loot{Items: slotsToProto(v.Items)})
}

func (lootCodec) Decode(b []byte) (Loot, error) {
	var l game.Loot
	if err := pb.Unmarshal(b, &l); err != nil {
		return Loot{}, err
	}
	items := make([]ItemStack, 0, len(l.Items))
	for _, s := range l.Items {
		items = append(items, ItemStack{Kind: s.Kind, Count: int(s.Count), MaxStack: int(s.MaxStack), Durability: int(s.Durability)})
	}
	return Loot{Items: items}, nil
}

func RegisterInventory(w *ecs.World) {
	ecs.RegisterComponent(w, "Inventory", inventoryCodec{})
}

func RegisterLoot(w *ecs.World) {
	ecs.RegisterComponent(w, "Loot", lootCodec{})
}

// RegisterLootable 注册 Lootable 组件 codec。
func RegisterLootable(w *ecs.World) {
	ecs.RegisterComponent(w, "Lootable", lootableCodec{})
}
