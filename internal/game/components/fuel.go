package components

import (
	"encoding/binary"
	"fmt"

	"starve/internal/ecs"
)

// Fuel 燃料（火堆等）：剩余可燃时长 + 上限 + 重新点燃所需的热量参数。
//
// 单位是 **tick 的燃烧时长**，不是"占火堆多少份额"：
// 份额制在改了火堆上限或消耗速率之后含义会悄悄漂移，而"这块木头能顶 60 秒"
// 是策划和玩家都能直接对上的常量（20Hz 下 1 tick = 50ms）。
//
// 为什么把热量参数也塞在这里：熄灭的实现是**移除 HeatSource**（客户端靠组件的
// 增删看火焰，不需要新字段），组件一旦删掉就没人记得这个火堆原本多热。
// 把"重新点燃需要什么"跟燃料放在一起，熄灭/复燃才是可逆的一对操作。
type Fuel struct {
	Cur          int // 剩余燃料（tick）；<= 0 = 已熄灭
	Max          int // 燃料上限（tick）：添柴封顶，避免无限堆柴；<= 0 = 不封顶（旧档兜底）
	HeatStrength int // 点燃时写入 HeatSource.Strength
	HeatRadius   int // 点燃时写入 HeatSource.Radius
}

// fuelCodec 手写定长二进制编解码（4 × uint32，共 16 字节）。
//
// 为什么不用 protobuf：本次不动 pkg/proto（协议层冻结），而组件快照/存档走的是
// 通用 Codec 接口（见 ecs.Register + pushInterestDeltas），并不要求 protobuf。
// 定长、无变体、字段顺序与结构体声明一致；Fuel 只是运行期状态，
// 不做向前兼容——缺字段的旧档当作"没有火堆"处理即可。
type fuelCodec struct{}

const fuelCodecSize = 16

func (fuelCodec) Encode(v Fuel) ([]byte, error) {
	b := make([]byte, fuelCodecSize)
	binary.LittleEndian.PutUint32(b[0:], uint32(int32(v.Cur)))
	binary.LittleEndian.PutUint32(b[4:], uint32(int32(v.Max)))
	binary.LittleEndian.PutUint32(b[8:], uint32(int32(v.HeatStrength)))
	binary.LittleEndian.PutUint32(b[12:], uint32(int32(v.HeatRadius)))
	return b, nil
}

func (fuelCodec) Decode(b []byte) (Fuel, error) {
	if len(b) < fuelCodecSize {
		return Fuel{}, fmt.Errorf("fuel codec: 载荷 %d 字节，需要 %d", len(b), fuelCodecSize)
	}
	return Fuel{
		Cur:          int(int32(binary.LittleEndian.Uint32(b[0:]))),
		Max:          int(int32(binary.LittleEndian.Uint32(b[4:]))),
		HeatStrength: int(int32(binary.LittleEndian.Uint32(b[8:]))),
		HeatRadius:   int(int32(binary.LittleEndian.Uint32(b[12:]))),
	}, nil
}

func RegisterFuel(w *ecs.World) {
	ecs.RegisterComponent(w, "Fuel", fuelCodec{})
}

// RelightHeatSource 点燃：按 Fuel 里存的热量参数恢复 HeatSource。
// 幂等——已点燃就直接返回，所以"每 tick 保证点燃状态"的写法不会重复挂组件。
//
// 契约：**"着/不着"的唯一载体是 HeatSource 组件的有无**，不是 Fuel.Cur 的符号。
// 客户端靠快照的 removed 通道看到火焰消失，任何"用别的字段表达熄灭了"的做法
// 都会让火焰表现和实际供暖状态各说各话。
func RelightHeatSource(w *ecs.World, e ecs.Entity, f *Fuel) {
	if ecs.Has[HeatSource](w, e) {
		return
	}
	ecs.Add(w, e, HeatSource{Strength: f.HeatStrength, Radius: f.HeatRadius})
}

// ExtinguishHeatSource 熄灭：移除 HeatSource（幂等，本来没点燃时 ecs.Remove 直接返回）。
// 移除会标脏并让编码失败，走增量快照的 RemovedComponents 通道下发。
func ExtinguishHeatSource(w *ecs.World, e ecs.Entity) {
	ecs.Remove[HeatSource](w, e)
}
