package components

import (
	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	game "starve/pkg/proto/game"
)

// AOI 感知组件。两种"范围"语义不同，业界通常也是分开的：
//
//   - **感知半径** Perception：我能**发现**谁（决定追击/潜行）。通常较小，
//     太大就没有潜行感、怪会隔着半张图扑过来。
//   - **仇恨传播半径** Threat：同伴被打时，多大范围内的同类会被"通知"。
//     通常较大，否则"群体仇恨"只有身边一两只响应，狼群形同虚设。
//
// Visible = 在 Threat 半径内的 liveable（**超集**，AOISystem 每轮重建，实体 id 升序）。
// 为什么用 Threat 当查询半径、而不是各查一份：
//
//	AOI 的代价 ∝ 半径²，查两份等于付出两份方格标记成本。而"是否在感知内"
//	只需要对 Visible 再做一次**精确距离比较**即可判定（O(Visible)，很便宜）。
//	所以这里只保留一份 Visible（按较大半径），消费方按需用 InPerception 过滤。
//
// InPerception 是**查询方法**而非缓存字段：它依赖查询者自己的位置，
// 而 Visible 已经是按 Threat 半径算好的候选集。
type AOI struct {
	// Radius 是 Visible 的覆盖半径（= max(Perception, Threat)，见 world.seed）。
	Radius int
	// Perception 是真正的感知半径（发现敌人的范围），<= Radius。
	// 0 表示"不感知"（被动生物）。
	Perception int
	// Threat 是仇恨传播半径（同伴被打时的通知范围）。
	Threat int
	// Visible 是在 Radius 内的 liveable（超集）。
	Visible []ecs.Entity
}

// PerceptionRadius 返回生效的感知半径。
//
// 回退规则：Perception 未设置（0）时用 Radius —— 这样"只写 Radius"的旧写法
// （测试与少量内部构造）语义不变，不会因为新增字段而让感知突然变成 0。
// 生产路径由 seed.go 显式写入三个字段，不依赖这个回退。
func (a *AOI) PerceptionRadius() int {
	if a.Perception > 0 {
		return a.Perception
	}
	return a.Radius
}

// ThreatRadius 返回生效的仇恨传播半径（同样回退到 Radius）。
func (a *AOI) ThreatRadius() int {
	if a.Threat > 0 {
		return a.Threat
	}
	return a.Radius
}

// InPerception 报告 target 是否落在**感知半径**内（需要 target 坐标）。
//
// 用切比雪夫距离，与 AOI 的正方形覆盖口径一致。
func (a *AOI) InPerception(self, target Position) bool {
	r := a.PerceptionRadius()
	if r <= 0 {
		return false
	}
	return chebyshev(self.X, self.Y, target.X, target.Y) <= r
}

// InThreatRange 报告 target 是否落在**仇恨传播半径**内。
func (a *AOI) InThreatRange(self, target Position) bool {
	r := a.ThreatRadius()
	if r <= 0 {
		return false
	}
	return chebyshev(self.X, self.Y, target.X, target.Y) <= r
}

// aoiCodec 按 includeVisible（debug 开关）决定是否编码 Visible。
type aoiCodec struct{ includeVisible bool }

func (c aoiCodec) Encode(v AOI) ([]byte, error) {
	out := &game.AOI{
		Radius:     int32(v.Radius),
		Perception: int32(v.Perception),
		Threat:     int32(v.Threat),
	}
	if c.includeVisible {
		for _, e := range v.Visible {
			out.Visible = append(out.Visible, uint64(e))
		}
	}
	return pb.Marshal(out)
}

func (aoiCodec) Decode(b []byte) (AOI, error) {
	var m game.AOI
	if err := pb.Unmarshal(b, &m); err != nil {
		return AOI{}, err
	}
	out := AOI{
		Radius:     int(m.Radius),
		Perception: int(m.Perception),
		Threat:     int(m.Threat),
	}
	for _, id := range m.Visible {
		out.Visible = append(out.Visible, ecs.Entity(id))
	}
	return out, nil
}

// DebugFlags 调试开关（世界级 Resource）。
type DebugFlags struct {
	AOI bool // 调试：AOI.Visible 随快照下发（AOISystem 变更时 MarkDirty）
	// Collision 调试：每 tick 给实体挂 DebugShape（简化碰撞体）随快照下发，
	// 客户端画线框核对"碰撞体是不是刚好包住渲染模型"（systems.DebugShapeSystem）。
	Collision bool
}

func RegisterAOI(w *ecs.World, debug bool) {
	ecs.RegisterComponent(w, "AOI", aoiCodec{includeVisible: debug})
}
