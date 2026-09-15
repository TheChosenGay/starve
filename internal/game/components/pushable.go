package components

import (
	"starve/internal/ecs"
)

// Pushable 可推动标记：玩家顶着它时，它能被推动。
//
// 语义与"会不会自己动"**正交**，这是两个不同的维度：
//
//	              会自己动(有 Moveable)   不会自己动(无 Moveable)
//	推得动         玩家 / 动物              船 / 浮木 / 可推的箱子
//	推不动         理论上不存在            树 / 岩 / 建筑 / 工作站
//
// 所以：
//   - **会不会自己动** 看有没有 Moveable 组件。索引的动态层、ORCA 邻居表
//     都以 Moveable 为判据（ORCA 是"相互移动的物体"之间的互惠避让，
//     对不会动的东西没有意义）。
//   - **推不推得动** 看有没有 Pushable。树/墙/建筑没有 Pushable → 推不动；
//     船即使没有 Moveable（不划就不动），只要有 Pushable 就能被顶走。
//
// 注意：一个实体**暂时**可动（比如船被推起来的瞬间）由"临时挂上 Moveable"
// 表达，而不是靠这个标记——见 Expire / Condition 机制。
// 这样"能不能动"是状态，"为什么能动"由组件组合表达，不需要额外的枚举。
type Pushable struct{}

// IsPushable 判断实体是否可被推动。
func IsPushable(w *ecs.World, e ecs.Entity) bool { return ecs.Has[Pushable](w, e) }

// CanSelfMove 判断实体会不会自己动（= 有 Moveable 组件）。
//
// 这是索引动态层与 ORCA 邻居表的**唯一判据**。之前用 Static/Dynamic 两个
// 互斥 tag 表达，但那个语义是"位置会不会变"，与"推不推得动"混在了一起——
// 船（不自己动但推得动）就无法表达。现在拆成两个正交维度：
//   - CanSelfMove  = 有 Moveable
//   - IsPushable   = 有 Pushable
func CanSelfMove(w *ecs.World, e ecs.Entity) bool { return ecs.Has[Moveable](w, e) }

func RegisterPushable(w *ecs.World) { ecs.RegisterComponent(w, "Pushable", pushableCodec{}) }

// 空组件也需要 codec（进存档/快照），编解码都是空字节。
type pushableCodec struct{}

func (pushableCodec) Encode(Pushable) ([]byte, error) { return nil, nil }
func (pushableCodec) Decode([]byte) (Pushable, error) { return Pushable{}, nil }
