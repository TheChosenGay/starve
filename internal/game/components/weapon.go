package components

import (
	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// Weapon 攻击能力（范围/伤害/冷却）；生物生成时从模板挂，玩家装备武器后续复用。
type Weapon struct {
	AttackRange    int
	AttackDamage   int
	AttackCooldown int // 攻击间隔（tick）
}

type weaponCodec struct{}

func (weaponCodec) Encode(v Weapon) ([]byte, error) {
	return pb.Marshal(&game.Weapon{
		AttackRange:    int32(v.AttackRange),
		AttackDamage:   int32(v.AttackDamage),
		AttackCooldown: int32(v.AttackCooldown),
	})
}

func (weaponCodec) Decode(b []byte) (Weapon, error) {
	var m game.Weapon
	if err := pb.Unmarshal(b, &m); err != nil {
		return Weapon{}, err
	}
	return Weapon{AttackRange: int(m.AttackRange), AttackDamage: int(m.AttackDamage), AttackCooldown: int(m.AttackCooldown)}, nil
}
