package interactive

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// 主动能力（-er）：作用方装备实体携带，装备时复制到作用者。
type Chopper struct{ Efficiency, Range, Durability int }
type Miner struct{ Efficiency, Range, Durability int }
type Picker struct{ Efficiency, Range, Durability int }

// 受激能力（-able）：目标携带，接受对应主动能力作用。
type Choppable struct {
	Kind              game.ItemKind
	WorkLeft, MaxWork int
}
type Minable struct {
	Kind              game.ItemKind
	WorkLeft, MaxWork int
}
type Pickable struct {
	Kind              game.ItemKind
	WorkLeft, MaxWork int
}

// Attacker 主动攻击能力（-er）：伤害/距离/冷却。攻击作用于目标 Health，由 Health 减免。
// 流程与工作量型（砍/挖/采）不同，注册独立的 AttackPair（见 world/interact.go）。
type Attacker struct {
	AttackDamage   int
	AttackRange    int
	AttackCooldown int
}

// Use 消耗一次主动能力：耐久 -1（无耐久的裸手/爪子不消耗）；返回是否损坏。
// 组件自己处理状态变更，行为只调用不关心数值。
func (c *Chopper) Use(w *ecs.World, e ecs.Entity) bool {
	if c.Durability > 0 {
		c.Durability--
		ecs.MarkDirty[Chopper](w, e)
	}
	return c.Durability == 0
}

func (c *Miner) Use(w *ecs.World, e ecs.Entity) bool {
	if c.Durability > 0 {
		c.Durability--
		ecs.MarkDirty[Miner](w, e)
	}
	return c.Durability == 0
}

func (c *Picker) Use(w *ecs.World, e ecs.Entity) bool {
	if c.Durability > 0 {
		c.Durability--
		ecs.MarkDirty[Picker](w, e)
	}
	return c.Durability == 0
}

// BeChopped 被砍伐：扣工作量，返回是否耗尽（组件内部处理自己的状态）。
func (c *Choppable) BeChopped(w *ecs.World, e ecs.Entity, eff int) bool {
	c.WorkLeft -= eff
	depleted := c.WorkLeft <= 0
	if depleted {
		c.WorkLeft = 0
	}
	ecs.MarkDirty[Choppable](w, e)
	return depleted
}

// BeMined 被挖掘：扣工作量，返回是否耗尽。
func (c *Minable) BeMined(w *ecs.World, e ecs.Entity, eff int) bool {
	c.WorkLeft -= eff
	depleted := c.WorkLeft <= 0
	if depleted {
		c.WorkLeft = 0
	}
	ecs.MarkDirty[Minable](w, e)
	return depleted
}

// BePicked 被采集：扣工作量，返回是否耗尽。
func (c *Pickable) BePicked(w *ecs.World, e ecs.Entity, eff int) bool {
	c.WorkLeft -= eff
	depleted := c.WorkLeft <= 0
	if depleted {
		c.WorkLeft = 0
	}
	ecs.MarkDirty[Pickable](w, e)
	return depleted
}

type capabilityCodec[T any] struct {
	encode func(v T) ([]byte, error)
	decode func(b []byte) (T, error)
}

func (c capabilityCodec[T]) Encode(v T) ([]byte, error) { return c.encode(v) }
func (c capabilityCodec[T]) Decode(b []byte) (T, error) { return c.decode(b) }

func capEncode(efficiency, rng, dur int) ([]byte, error) {
	return pb.Marshal(&game.Capability{Efficiency: int32(efficiency), Range: int32(rng), Durability: int32(dur)})
}

func capDecode(b []byte) (eff, rng, dur int, err error) {
	var m game.Capability
	if err = pb.Unmarshal(b, &m); err != nil {
		return
	}
	return int(m.Efficiency), int(m.Range), int(m.Durability), nil
}

func workTargetEncode(kind game.ItemKind, left, max int) ([]byte, error) {
	return pb.Marshal(&game.WorkTarget{Kind: kind, WorkLeft: int32(left), MaxWork: int32(max)})
}

func workTargetDecode(b []byte) (kind game.ItemKind, left, max int, err error) {
	var m game.WorkTarget
	if err = pb.Unmarshal(b, &m); err != nil {
		return
	}
	return m.Kind, int(m.WorkLeft), int(m.MaxWork), nil
}

type attackerCodec struct{}

func (attackerCodec) Encode(v Attacker) ([]byte, error) {
	return pb.Marshal(&game.Attacker{AttackDamage: int32(v.AttackDamage), AttackRange: int32(v.AttackRange), AttackCooldown: int32(v.AttackCooldown)})
}

func (attackerCodec) Decode(b []byte) (Attacker, error) {
	var m game.Attacker
	if err := pb.Unmarshal(b, &m); err != nil {
		return Attacker{}, err
	}
	return Attacker{AttackDamage: int(m.AttackDamage), AttackRange: int(m.AttackRange), AttackCooldown: int(m.AttackCooldown)}, nil
}

// RegisterAttacker 注册 Attacker 组件 codec。
func RegisterAttacker(w *ecs.World) {
	ecs.RegisterComponent(w, "Attacker", attackerCodec{})
}

// RegisterEquip 注册 Equip 组件 codec。
func RegisterEquip(w *ecs.World) {
	ecs.RegisterComponent(w, "Equip", equipCodec{})
}

// RegisterChopper 注册 Chopper 组件 codec。
func RegisterChopper(w *ecs.World) {
	ecs.RegisterComponent(w, "Chopper", capabilityCodec[Chopper]{
		encode: func(v Chopper) ([]byte, error) { return capEncode(v.Efficiency, v.Range, v.Durability) },
		decode: func(b []byte) (Chopper, error) {
			eff, r, d, err := capDecode(b)
			return Chopper{Efficiency: eff, Range: r, Durability: d}, err
		},
	})
}

// RegisterMiner 注册 Miner 组件 codec。
func RegisterMiner(w *ecs.World) {
	ecs.RegisterComponent(w, "Miner", capabilityCodec[Miner]{
		encode: func(v Miner) ([]byte, error) { return capEncode(v.Efficiency, v.Range, v.Durability) },
		decode: func(b []byte) (Miner, error) {
			eff, r, d, err := capDecode(b)
			return Miner{Efficiency: eff, Range: r, Durability: d}, err
		},
	})
}

// RegisterPicker 注册 Picker 组件 codec。
func RegisterPicker(w *ecs.World) {
	ecs.RegisterComponent(w, "Picker", capabilityCodec[Picker]{
		encode: func(v Picker) ([]byte, error) { return capEncode(v.Efficiency, v.Range, v.Durability) },
		decode: func(b []byte) (Picker, error) {
			eff, r, d, err := capDecode(b)
			return Picker{Efficiency: eff, Range: r, Durability: d}, err
		},
	})
}

// RegisterChoppable 注册 Choppable 组件 codec。
func RegisterChoppable(w *ecs.World) {
	ecs.RegisterComponent(w, "Choppable", capabilityCodec[Choppable]{
		encode: func(v Choppable) ([]byte, error) { return workTargetEncode(v.Kind, v.WorkLeft, v.MaxWork) },
		decode: func(b []byte) (Choppable, error) {
			k, l, m, err := workTargetDecode(b)
			return Choppable{Kind: k, WorkLeft: l, MaxWork: m}, err
		},
	})
}

// RegisterMinable 注册 Minable 组件 codec。
func RegisterMinable(w *ecs.World) {
	ecs.RegisterComponent(w, "Minable", capabilityCodec[Minable]{
		encode: func(v Minable) ([]byte, error) { return workTargetEncode(v.Kind, v.WorkLeft, v.MaxWork) },
		decode: func(b []byte) (Minable, error) {
			k, l, m, err := workTargetDecode(b)
			return Minable{Kind: k, WorkLeft: l, MaxWork: m}, err
		},
	})
}

// RegisterPickable 注册 Pickable 组件 codec。
func RegisterPickable(w *ecs.World) {
	ecs.RegisterComponent(w, "Pickable", capabilityCodec[Pickable]{
		encode: func(v Pickable) ([]byte, error) { return workTargetEncode(v.Kind, v.WorkLeft, v.MaxWork) },
		decode: func(b []byte) (Pickable, error) {
			k, l, m, err := workTargetDecode(b)
			return Pickable{Kind: k, WorkLeft: l, MaxWork: m}, err
		},
	})
}
