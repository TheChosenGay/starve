package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// Explosive 爆炸属性：这件东西炸开时有多大威力。
//
// 为什么做成**独立组件**（而不是物品模板里的一个字段）：
//   - 与 Creature/Weapon 的做法一致：模板 → 组件，运行时可以改；
//   - 爆炸不只属于"投掷物"——油桶、炸药桶、Boss 的自爆技能
//     都可能需要爆炸，挂个组件比各自定义一套字段更通用；
//   - 判定与表现都读组件，避免"模板里有、实体上没有"的脱节。
//
// 生成时由物品模板的 explode 段拷贝（见 config.ItemTemplate.Explode）。
type Explosive struct {
	// Radius 爆炸半径（格）：以爆心为球心、向上半球的半径。
	Radius float64
	// Damage 对范围内每个目标的伤害（点）。
	Damage int
	// FuseTicks 引信时长（tick）：从落地到爆炸的延迟。
	// 0 = 落地立刻炸（投掷物用这个）；>0 用于"放下后过一会儿炸"的炸药桶。
	FuseTicks int
	// Knockback 击退强度（格）：爆心附近的实体被推离的最大距离。
	// 0 = 不击退。
	Knockback float64
}

// 缺省爆炸参数（模板未指定时用）。
//
// 放在组件包而不是 systems：这些是**数据默认值**，
// 与 Explosive 的字段语义在一起更好维护。
const (
	// DefaultBlastRadius 缺省爆炸半径（格）。
	DefaultBlastRadius = 2.5
	// DefaultBlastDamage 缺省爆炸伤害（点）。
	DefaultBlastDamage = 6
	// DefaultBlastKnockback 缺省击退强度（格）。
	DefaultBlastKnockback = 1.5
)

// Usable 实现 interactive.Actived：有爆炸属性的东西可以被"引爆"。
func (Explosive) Usable(w *ecs.World, e ecs.Entity) bool { return true }

// FuseRemaining 是引信剩余（挂在实体上，>0 时倒计时）。
//
// 为什么单独一个组件而不是 Explosive 里的字段：Explosive 是**静态属性**
// （从模板拷贝、不该被运行时改），而引信是**运行时状态**。
// 混在一起会让"同一个模板实例化的两个炸药桶"共享倒计时。
type FuseRemaining struct {
	Ticks int
}

type explosiveCodec struct{}

func (explosiveCodec) Encode(v Explosive) ([]byte, error) {
	return pb.Marshal(&game.Explosive{
		Radius:    float32(v.Radius),
		Damage:    int32(v.Damage),
		FuseTicks: int32(v.FuseTicks),
		Knockback: float32(v.Knockback),
	})
}

func (explosiveCodec) Decode(b []byte) (Explosive, error) {
	var m game.Explosive
	if err := pb.Unmarshal(b, &m); err != nil {
		return Explosive{}, err
	}
	return Explosive{
		Radius:    float64(m.Radius),
		Damage:    int(m.Damage),
		FuseTicks: int(m.FuseTicks),
		Knockback: float64(m.Knockback),
	}, nil
}

type fuseCodec struct{}

func (fuseCodec) Encode(v FuseRemaining) ([]byte, error) {
	return pb.Marshal(&game.FuseRemaining{Ticks: int32(v.Ticks)})
}

func (fuseCodec) Decode(b []byte) (FuseRemaining, error) {
	var m game.FuseRemaining
	if err := pb.Unmarshal(b, &m); err != nil {
		return FuseRemaining{}, err
	}
	return FuseRemaining{Ticks: int(m.Ticks)}, nil
}

func RegisterExplosive(w *ecs.World) { ecs.RegisterComponent(w, "Explosive", explosiveCodec{}) }
func RegisterFuseRemaining(w *ecs.World) {
	ecs.RegisterComponent(w, "FuseRemaining", fuseCodec{})
}
