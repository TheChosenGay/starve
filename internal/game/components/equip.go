package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Slot 装备槽位（稳定枚举；未来在此扩展：披风/戒指/饰品等）。
type Slot uint8

const (
	SlotHead Slot = iota + 1 // 头戴
	SlotHand                 // 手持
	SlotBody                 // 身上穿
)

func (s Slot) String() string {
	switch s {
	case SlotHead:
		return "head"
	case SlotHand:
		return "hand"
	case SlotBody:
		return "body"
	}
	return "unknown"
}

// All 槽位按施加顺序枚举（后施加覆盖先施加）。
func All() []Slot { return []Slot{SlotHead, SlotHand, SlotBody} }

// Equip 装备组件：三个槽位各持有一个装备实体（0 = 空槽）。
type Equip struct {
	Head, Hand, Body ecs.Entity
}

func (e *Equip) Item(s Slot) ecs.Entity {
	switch s {
	case SlotHead:
		return e.Head
	case SlotHand:
		return e.Hand
	case SlotBody:
		return e.Body
	}
	return 0
}

func (e *Equip) Set(s Slot, item ecs.Entity) {
	switch s {
	case SlotHead:
		e.Head = item
	case SlotHand:
		e.Hand = item
	case SlotBody:
		e.Body = item
	}
}

type equipCodec struct{}

func (equipCodec) Encode(v Equip) ([]byte, error) {
	return pb.Marshal(&game.Equip{Head: uint64(v.Head), Hand: uint64(v.Hand), Body: uint64(v.Body)})
}

func (equipCodec) Decode(b []byte) (Equip, error) {
	var m game.Equip
	if err := pb.Unmarshal(b, &m); err != nil {
		return Equip{}, err
	}
	return Equip{Head: ecs.Entity(m.Head), Hand: ecs.Entity(m.Hand), Body: ecs.Entity(m.Body)}, nil
}

// RegisterEquip 注册 Equip 组件 codec。
func RegisterEquip(w *ecs.World) {
	ecs.RegisterComponent(w, "Equip", equipCodec{})
}
