package components

import (
	"sort"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// EffectOrder 效果类型（单一事实来源 = proto 枚举）。
// 枚举值同时是确定性结算顺序：EffectSystem 按升序遍历，保证重放一致。
type EffectOrder = game.EffectOrder

// 常用效果常量（配置表/命令/效果实现里用这些名字）。
const (
	EffectSpeed  = game.EffectOrder_EFFECT_ORDER_SPEED
	EffectPoison = game.EffectOrder_EFFECT_ORDER_POISON
	EffectCold   = game.EffectOrder_EFFECT_ORDER_COLD
	EffectHeat   = game.EffectOrder_EFFECT_ORDER_HEAT
)

// EffectOrderByName 配置字符串 → 效果枚举（新效果 = 枚举值 + 这里加一行 + 效果实现）。
var EffectOrderByName = map[string]EffectOrder{
	"speed":  EffectSpeed,
	"poison": EffectPoison,
}

// EffectState 单个效果的覆盖状态：多来源计数 + 聚合参数（多来源求和）。
// 例如两个毒源（param 1 和 2）→ Count=2, Param=3（每 tick 扣 3 血）。
type EffectState struct {
	Count int32
	Param int32
}

// Effects 实体当前生效效果（覆盖状态）：Count=0 表示无。
// 多来源叠加用计数表达：多个发射器/地块同时给同一效果时 Count++，
// 减到 0 才 OnExit——不需要记录"上次在哪个格"。
// 数据组件在 components 包；行为（接口/注册表/具体效果）在 components/effect 子包。
type Effects struct {
	Active map[EffectOrder]EffectState
}

// Has 是否处于某效果覆盖中。
func (e *Effects) Has(order EffectOrder) bool { return e.Active[order].Count > 0 }

type effectsCodec struct{}

func (effectsCodec) Encode(v Effects) ([]byte, error) {
	out := &game.Effects{}
	for _, o := range sortedEffectOrders(v.Active) {
		st := v.Active[o]
		out.Active = append(out.Active, &game.EffectActive{Order: o, Count: st.Count, Param: st.Param})
	}
	return pb.Marshal(out)
}

func (effectsCodec) Decode(b []byte) (Effects, error) {
	var m game.Effects
	if err := pb.Unmarshal(b, &m); err != nil {
		return Effects{}, err
	}
	out := Effects{Active: make(map[EffectOrder]EffectState, len(m.Active))}
	for _, a := range m.Active {
		if a != nil && a.Order != 0 && a.Count > 0 {
			out.Active[a.Order] = EffectState{Count: a.Count, Param: a.Param}
		}
	}
	return out, nil
}

// sortedEffectOrders 返回 map 中按枚举值升序的效果列表（确定性遍历）。
func sortedEffectOrders(m map[EffectOrder]EffectState) []EffectOrder {
	orders := make([]EffectOrder, 0, len(m))
	for o := range m {
		if o != 0 && m[o].Count > 0 {
			orders = append(orders, o)
		}
	}
	sort.Slice(orders, func(i, j int) bool { return orders[i] < orders[j] })
	return orders
}

// EffectInstance 一个效果实例：类型 + 参数（毒伤量、速度修正百分比等）。
// 效果强度走 param（配置/发射器携带），同一效果只写一个枚举值。
type EffectInstance struct {
	Order EffectOrder
	Param int
}

// EffectEmitter 效果发射器（植物/火堆等实体挂载）：
// Effects 为效果集合，Radius 为作用半径（0 = 仅影响自身所在格）。
// 地块效果不走本组件，在 MapData.TileEffects（世界资源，见 world 包）。
type EffectEmitter struct {
	Effects []EffectInstance
	Radius  int
}

type effectEmitterCodec struct{}

func (effectEmitterCodec) Encode(v EffectEmitter) ([]byte, error) {
	effs := append([]EffectInstance(nil), v.Effects...)
	sort.Slice(effs, func(i, j int) bool { return effs[i].Order < effs[j].Order })
	out := &game.EffectEmitter{Radius: int32(v.Radius)}
	for _, ins := range effs {
		out.Effects = append(out.Effects, &game.EffectInstance{Order: ins.Order, Param: int32(ins.Param)})
	}
	return pb.Marshal(out)
}

func (effectEmitterCodec) Decode(b []byte) (EffectEmitter, error) {
	var m game.EffectEmitter
	if err := pb.Unmarshal(b, &m); err != nil {
		return EffectEmitter{}, err
	}
	out := EffectEmitter{Radius: int(m.Radius)}
	for _, ins := range m.Effects {
		if ins != nil {
			out.Effects = append(out.Effects, EffectInstance{Order: ins.Order, Param: int(ins.Param)})
		}
	}
	return out, nil
}

func RegisterEffects(w *ecs.World) {
	ecs.RegisterComponent(w, "Effects", effectsCodec{})
}

func RegisterEffectEmitter(w *ecs.World) {
	ecs.RegisterComponent(w, "EffectEmitter", effectEmitterCodec{})
}
