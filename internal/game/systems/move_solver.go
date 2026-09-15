package systems

import (
	"math"
	"sort"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// MoveInput 是一次移动求解的输入（"我想怎么动"）。
type MoveInput struct {
	// DesiredX/DesiredY 是**期望位移**（格，已含速度、坡度、dt、对角归一化）。
	// 这是阶段①的产物：玩家来自输入方向，AI 来自路径队首。
	DesiredX, DesiredY float64
	// DirX/DirY 是意图方向（-1/0/1），用于胶囊朝向与路径点弹出。
	DirX, DirY int
	// Speed 是当前有效速度（格/秒），ORCA 需要它作上界。
	Speed float64
	// DT 是本 tick 时长（秒）。
	DT float64
}

// MoveResult 是三阶段求解的结果。
type MoveResult struct {
	// FinalX/FinalY 是**最终位移**（格）：阶段③之后真正要提交的量。
	FinalX, FinalY float64
	// VelX/VelY 是最终速度（格/秒）= 位移 / dt，写回 Moveable 供客户端复现。
	VelX, VelY float64
	// SlideX/SlideY 是阶段②（静态滑掠）后的位移，保留供调试/测试。
	SlideX, SlideY float64
	// Hits 是阶段②的接触次数。
	Hits int
}

// MoveSolver 是**三阶段移动求解器**，服务端 MoveSystem 与 TUI 本地预测共用同一份。
//
// 三个阶段（顺序固定，缺一不可）：
//
//	① desired：意图位移（速度 × 坡度 × dt，对角归一化）—— 调用方算好传入；
//	② slide  ：对**静态**碰撞体做连续扫掠 + 沿切面投影（硬约束，绝不穿模）；
//	③ avoid  ：ORCA 动态避让（软约束，只对其他动态体），产出可任意方向的最终速度。
//
// 为什么把静态和动态分开解：静态是"墙"，必须硬挡；动态是"人"，应该互相让开。
// 早先混在一次解算里时，相向而行的两人会被同时硬挡、再被"起点重叠推出"弹回，
// 表现为每 tick 来回抖动（实测重叠 0.385 格）。
type MoveSolver struct {
	// ORCA 是阶段③的求解器（可为 nil = 跳过避让，退化成纯静态滑掠）。
	ORCA *ORCASolver
	// NeighborRadius 是收集 ORCA 邻居的查询半径（格）。
	//
	// 留 0 表示**由 τ 与速度自动推导**（推荐）：radius = 2 × 速度上限 × τ。
	// 手动写死很容易与 τ 脱节——曾经写死 20 格（对应 τ=2s 的最坏位移），
	// 结果 τ 改成 0.5s 后半径仍是 20，白白多查 18 倍面积的邻居。
	NeighborRadius float64
	// SpeedCap 是推导查询半径用的速度上限（格/秒）。0 = 用 ORCA 的缺省 10。
	SpeedCap float64
	// Debug 打开时把中间量记进 MoveResult（生产环境可关，避免多余分配）。
	Debug bool

	// nbBuf 是邻居收集的复用缓冲（见 collectNeighbors）。
	nbBuf []ORCABody
	// orcaBuf 是喂给 ORCA 的邻居缓冲（避免每次转存都分配）。
	orcaBuf []ORCABody
}

// NewMoveSolver 建一个三阶段求解器。neighborRadius ≤ 0 时按 τ 与速度上限自动推导。
func NewMoveSolver(orca *ORCASolver, neighborRadius float64) *MoveSolver {
	s := &MoveSolver{ORCA: orca, NeighborRadius: neighborRadius, Debug: true}
	s.NeighborRadius = s.neighborRadius()
	return s
}

// neighborRadius 返回实际使用的邻居查询半径（显式设置优先，否则按 τ 推导）。
//
// 推导依据：τ 秒内双方各自最多走 speed×τ，所以"可能相遇"的最远初距是
// (我的速度 + 对方速度) × τ。两边都用**同一速度**估计，即 2×speed×τ。
//
// speed 取**典型**速度（玩家基准 10 格/秒），不是极端上限：
// 半径是个静态配置（不能每帧按当前速度变，否则邻居集合会抖），
// 而按极端值（如吃了加速效果的 20）取会让半径翻倍、邻居翻 4 倍——
// 这正是之前 τ=2s + 20 格半径导致 1000 实体 88ms 的原因。
// 少数高速实体的避让稍晚一点是可接受的，比全体为最坏情况买单划算。
func (s *MoveSolver) neighborRadius() float64 {
	if s.NeighborRadius > 0 {
		return s.NeighborRadius
	}
	speed := s.SpeedCap
	if speed <= 0 {
		speed = typicalSpeed
	}
	tau := DefaultORCAOptions().TimeHorizon
	if s.ORCA != nil && s.ORCA.Opts.TimeHorizon > 0 {
		tau = s.ORCA.Opts.TimeHorizon
	}
	return 2 * speed * tau
}

// typicalSpeed 是推导查询半径用的**典型**速度（格/秒）：玩家基准 10。
// 见 neighborRadius 的注释——这里刻意不取极端上限。
const typicalSpeed = 10.0

// Solve 对单个实体求解本 tick 的移动。
//
// w/sim 提供世界上下文（碰撞索引、地图、动态体位置）；
// e 是移动实体自己；p 是它的位置；mv 是移动状态；col 是碰撞体（可为 nil）。
//
// 返回的 MoveResult 由调用方负责提交（写 Position/Sub）——求解本身不改状态，
// 这样服务端的"两阶段提交"与客户端的"预测后校正"都能复用。
func (s *MoveSolver) Solve(
	w *ecs.World, e ecs.Entity, p *components.Position, mv *components.Moveable,
	col *components.Collide, in MoveInput,
) MoveResult {
	res := MoveResult{}
	if in.DesiredX == 0 && in.DesiredY == 0 {
		// 完全静止：显式把速度清零（避免残留上 tick 的速度）。
		return res
	}
	// 当前连续位置（格，浮点）——胶囊/扫掠都基于它。
	wx := float64(p.X) + mv.SubX
	wy := float64(p.Y) + mv.SubY
	body := BodyOf(wx, wy, col)

	// ── 阶段②：静态碰撞（硬约束）────────────────────────────
	slideX, slideY := in.DesiredX, in.DesiredY
	if idx, ok := ecs.TryResource[collision.Index](w); ok {
		endX, endY, hits := idx.SlideStatic(body, in.DesiredX, in.DesiredY)
		slideX, slideY = endX-body.X, endY-body.Z
		res.Hits = hits
	}
	res.SlideX, res.SlideY = slideX, slideY

	// ── 阶段③：ORCA 动态避让（软约束）──────────────────────
	finalX, finalY := slideX, slideY
	if s.ORCA != nil {
		if idx, ok := ecs.TryResource[collision.Index](w); ok {
			dt := in.DT
			if dt <= 0 {
				dt = 0.05
			}
			// 期望速度 = 阶段②的结果 / dt（"滑完之后我实际上想以多快走"）
			prefVX, prefVY := slideX/dt, slideY/dt
			maxSpeed := in.Speed
			if maxSpeed <= 0 {
				maxSpeed = math.Hypot(prefVX, prefVY)
			}
			// 当前速度：有历史就用历史，否则用期望速度（见下面 Agent 的注释）。
			selfVX, selfVY := mv.VelX, mv.VelY
			if selfVX == 0 && selfVY == 0 {
				selfVX, selfVY = prefVX, prefVY
			}
			self := Agent{
				X: wx, Z: wy,
				// 当前速度：优先用上 tick 的实际速度（RVO2 的基准）。
				// 但首 tick（或刚从静止起步）Vel 是 0，此时若把 0 当作"当前速度"，
				// ORCA 会按"我正在原地不动"去构造约束，把解压到极慢
				//（实测起步瞬间只走 13%）。所以没有历史速度时用期望速度兜底，
				// 语义是"我打算以这个速度走"，这与 RVO2 的 velocity_ 初值一致。
				VX: selfVX, VY: selfVY,
				PrefVX: prefVX, PrefVY: prefVY,
				Radius:   body.Radius + body.HalfLength, // 胶囊按外接圆处理
				MaxSpeed: maxSpeed,
			}
			neighbors := s.collectNeighbors(w, idx, e, wx, wy, body.Radius)
			if len(neighbors) > 0 {
				vx, vy := s.ORCA.Solve(self, neighbors)
				finalX, finalY = vx*dt, vy*dt
				res.VelX, res.VelY = vx, vy
			} else {
				res.VelX, res.VelY = prefVX, prefVY
			}
		}
	} else {
		res.VelX, res.VelY = velocityOf(finalX, finalY, in.DT)
	}
	res.FinalX, res.FinalY = finalX, finalY
	return res
}

// collectNeighbors 收集 ORCA 需要的动态邻居（不含自己）。
// 只取有 Collide + Dynamic 的实体；速度取它们 Moveable 的实际速度。
//
// 返回的切片由求解器复用（不是每次新分配）：本函数在每个 tick 被每个移动实体
// 调用一次，新分配会造成大量 GC 压力。调用方必须在下一次调用前用完。
func (s *MoveSolver) collectNeighbors(
	w *ecs.World, idx *collision.Index, self ecs.Entity, x, z, selfRadius float64,
) []ORCABody {
	ns := idx.Neighbors(x, z, s.neighborRadius(), self)
	if len(ns) == 0 {
		return nil
	}
	out := s.nbBuf[:0]
	for _, n := range ns {
		// **只把会自己动的实体当邻居**：ORCA 是"相互移动的物体"之间的互惠避让，
		// 对不会动的东西（树/墙/船这类没有 Moveable 的）谈"各让一半"没有意义。
		//
		// 早期版本把没有 Moveable 的也加进表里，MaxSpeed 算成 0 —— 那会在
		// ORCA 内部被当成"速度上限为 0"，产生一条退化的约束，白白干扰求解。
		if !ecs.Has[components.Moveable](w, n.Entity) {
			continue
		}
		mv := ecs.Get[components.Moveable](w, n.Entity)
		vx, vy := mv.VelX, mv.VelY
		maxSpd := mv.EffectiveSpeed
		if maxSpd <= 0 {
			maxSpd = mv.Speed
		}
		if maxSpd <= 0 {
			// 速度字段都缺失时按实际速度兜底（而不是 0，避免退化约束）。
			maxSpd = math.Hypot(vx, vy)
		}
		out = append(out, ORCABody{
			X: n.X, Z: n.Z, VX: vx, VY: vy,
			// 胶囊按外接圆处理（与 self 的半径算法一致，保证互惠对称）
			Radius: n.Radius + n.HalfLength, MaxSpeed: maxSpd,
		})
	}
	s.nbBuf = out
	return out
}

// velocityOf 由位移与 dt 反推速度（dt ≤ 0 时返回零）。
func velocityOf(dx, dy, dt float64) (float64, float64) {
	if dt <= 0 {
		return 0, 0
	}
	return dx / dt, dy / dt
}

// SortMoveEntities 返回本 tick 要参与移动解算的实体（按 id 排序，保证确定性）。
func SortMoveEntities(w *ecs.World) []ecs.Entity {
	var out []ecs.Entity
	ecs.Query[components.Moveable](w, func(e ecs.Entity, _ *components.Moveable) {
		out = append(out, e)
	})
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// DesiredDisplacement 是**阶段①**：由意图方向算出期望位移。
// 玩家用输入方向、AI 用路径队首；这里统一算速度×坡度×dt + 对角归一化。
func DesiredDisplacement(
	w *ecs.World, dir components.MoveDir, speed float64, dt float64, x, y float64,
) (float64, float64) {
	if dir.DX == 0 && dir.DY == 0 || speed <= 0 || dt <= 0 {
		return 0, 0
	}
	factor := 1.0
	if md, ok := ecs.TryResource[worldmap.MapData](w); ok {
		factor = worldmap.SlopeFactor(md, x, y, dir.DX, dir.DY)
	}
	dist := speed * factor * dt
	if dir.DX != 0 && dir.DY != 0 {
		dist *= 1 / math.Sqrt2 // 对角归一化：任意方向同速
	}
	return float64(dir.DX) * dist, float64(dir.DY) * dist
}
