package systems

import (
	"math"
	"os"
	"slices"
	"sort"
	"strconv"

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

	// NeighborRefreshTicks 是邻居表的刷新间隔（tick）。
	//   1  = 每 tick 重查（等价于不做缓存）
	//   ≥2 = 降频：非刷新 tick 直接用上轮的"有谁"，位置仍实时读
	// ≤0 表示用缺省值（defaultNeighborRefreshTicks = 4）。
	NeighborRefreshTicks int

	// nbBuf 是邻居收集的复用缓冲（见 collectNeighbors）。
	nbBuf []ORCABody
	// orcaBuf 是喂给 ORCA 的邻居缓冲（避免每次转存都分配）。
	orcaBuf []ORCABody

	// AOI 是**动态体专用网格**（OrcaAOI）。非 nil 时用它找邻居，
	// 静态障碍仍走 BVH 的形状层（两者职责不同：网格只管"谁会动"）。
	//
	// 为什么单独一个结构：BVH 是为"任意形状、任意分布"设计的通用索引，
	// 而 ORCA 的查询是"圆内有哪些动态体"——均匀网格在这种均匀分布下
	// 常数更小（离线对比：1000 实体 1.6ms vs BVH 3.8ms）。
	AOI *collision.OrcaAOI
	// gridLive/gridStamp/gridSeen 用于清理已消失的实体（死亡/移除/失去 Moveable）。
	gridLive  []ecs.Entity
	gridStamp map[ecs.Entity]struct{}
	gridSeen  map[ecs.Entity]struct{}
	// gridNbBuf 是网格查询结果的复用缓冲。
	gridNbBuf []collision.Neighbor

	// cache 是降频缓存：只缓存"有谁"，不缓存位置（见 neighbor_cache.go）。
	cache *neighborCacher
	// refreshThisTick 是本 tick 是否刷新邻居表（由 RefreshNeighborCache 在
	// tick 边界设定，collectNeighbors 只读）。
	refreshThisTick bool
	// tickBegun 表示调用方已经开过轮（调过 RefreshNeighborCache）。
	// 没有开轮时降到"每 tick 重查"，避免"某个调用方忘了开轮 -> 缓存永远空 ->
	// 预测看不到任何邻居"这类静默失效（TUI 的本地预测就踩过这个坑）。
	tickBegun bool
	// dynIDs 是上一轮刷新的邻居 id 列表（复用缓冲）。
	dynIDs []ecs.Entity
}

// defaultNeighborRefreshTicks 是邻居表的缺省刷新间隔。
//
// **缺省 1（不降频）** —— 实测降频在这套实现下是**负优化**，理由见下。
//
// 避让质量（world 包场景测试，12 只挤向同一点）：
//
//	间隔 1：中途 0.1418 / 稳定 0.6200  ← 基线
//	间隔 2：中途 0.1418 / 稳定 0.6200  ← 与基线完全相同
//	间隔 4：中途 0.2620 / 稳定 0.2620  ← 散不开
//	间隔 8：中途 0.0781 / 稳定 0.0781  ← 严重重叠
//
// 性能（1000 实体，完整三阶段）：
//
//	间隔 1：27.9 ms    间隔 2：37.3 ms   ← 更慢！
//
// 为什么更慢：非刷新 tick 必须**逐邻居**回查 ECS（Has/Get Moveable +
// 句柄取形状），而刷新路径下索引一次就返回了带 X/Z/Radius 的完整结构。
// 邻居数约 19 时，前者（每邻居 3 次 map 查找）比后者更贵。
// 瓶颈不在"查询次数"，而在"每个邻居要补多少次组件访问"。
//
// 结论：缓存要真正生效，得连**位置快照**一起缓存（但那会引入过期位置），
// 或者把逐邻居的组件访问合并掉。当前保持 1（不降频）——快且行为不变。
// 大半径+极密场景下可以试 >1，但要重新测。
const defaultNeighborRefreshTicks = 1

// NewMoveSolver 建一个三阶段求解器。neighborRadius ≤ 0 时按 τ 与速度上限自动推导。
//
// 邻居刷新间隔从 GATE_NEIGHBOR_REFRESH_TICKS 读（缺省 4）；设 1 可关掉降频
// 对照行为差异，设更大则在"实体极多"时进一步省算力。
func NewMoveSolver(orca *ORCASolver, neighborRadius float64) *MoveSolver {
	s := &MoveSolver{ORCA: orca, NeighborRadius: neighborRadius, Debug: true}
	s.NeighborRadius = s.neighborRadius()
	s.NeighborRefreshTicks = envIntOr("GATE_NEIGHBOR_REFRESH_TICKS", defaultNeighborRefreshTicks)
	return s
}

// envIntOr 读环境变量为整数（未设置/非法时用缺省值）。
func envIntOr(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// refreshRadius 是**刷新查询**用的半径：在精确半径上外扩一个刷新周期的位移，
// 使缓存成为"下次刷新前都够用"的超集（非刷新 tick 再按实时位置筛回精确半径）。
//
// 外扩量 = (我的速度 + 对方速度) × 刷新周期 = 2 × 速度 × (every × tick 时长)。
// 缺省 every=4、tick 50ms、速度 10 时 = 4 格。
func (s *MoveSolver) refreshRadius() float64 {
	every := float64(s.refreshEvery())
	const tickSec = 0.05 // 服务端固定 20Hz
	speed := s.SpeedCap
	if speed <= 0 {
		speed = typicalSpeed
	}
	// 两倍速度（双方都在动）× 周期时长
	return s.neighborRadius() + 2*speed*every*tickSec
}

// refreshEvery 返回实际使用的刷新间隔（≥1）。
func (s *MoveSolver) refreshEvery() int {
	if s.NeighborRefreshTicks <= 0 {
		return defaultNeighborRefreshTicks
	}
	return s.NeighborRefreshTicks
}

// RefreshNeighborCache 在 tick 边界调用一次，决定本轮是否重查邻居表。
// 返回 true 表示本轮会重查（调用方可据此做观测）。
//
// 之所以要显式区分"轮"，是因为一个 tick 内每个实体都会调 Solve 一次——
// 刷新判定必须**每 tick 一次**，而不是每个实体一次，否则每个实体都会刷新。
func (s *MoveSolver) RefreshNeighborCache() bool {
	every := s.refreshEvery()
	if s.cache == nil {
		s.cache = newNeighborCacher(every)
	}
	refresh := s.cache.shouldRefresh()
	if refresh {
		s.cache.beginRefresh()
	}
	s.refreshThisTick = refresh
	return refresh
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
//
// 判据 = 有 Moveable（"会不会自己动"）：ORCA 是相互移动的物体之间的互惠避让，
// 对不会动的东西没有意义——没有 Moveable 的候选直接跳过（否则 MaxSpeed=0
// 会在 ORCA 内部被当成"速度上限为 0"，产生退化的约束）。
//
// 返回的切片由求解器复用（不是每次新分配）：本函数在每个 tick 被每个移动实体
// 调用一次，新分配会造成大量 GC 压力。调用方必须在下一次调用前用完。
func (s *MoveSolver) collectNeighbors(
	w *ecs.World, idx *collision.Index, self ecs.Entity, x, z, selfRadius float64,
) []ORCABody {
	// 邻居表降频：刷新 tick 才真的查空间索引，其余 tick 直接用上轮的 id 列表。
	// 位置**每次实时取**（见下面邻居位置），否则会拿过期位置做避让。
	//
	// refresh 由 tick 边界决定（MoveSystem 调 RefreshNeighborCache），
	// 这里只读结果——早期版本在这里调 RefreshNeighborCache，于是**每个实体**
	// 都把 phase 往前推一格，降频彻底失效（且 cache 内容错乱）。
	// 没开轮（调用方没走 MoveSystem 的 tick 边界）时退化为每 tick 重查：
	// 宁可多算，也不要"缓存永远空 -> 看不到邻居"。
	// 走网格时不需要降频缓存：网格本身就是增量的，单次查询也足够便宜。
	if s.AOI != nil {
		return s.collectFromOrcaAOI(w, idx, self, x, z, s.nbBuf[:0])
	}
	refresh := s.refreshThisTick
	if !s.tickBegun {
		refresh = true
	}
	var ns []collision.Neighbor
	if refresh {
		// 刷新时查**外扩后的超集半径**：缓存要在下次刷新前一直够用，
		// 而下个周期内双方还会互相接近 (我的速度+对方速度)×周期 格。
		// 只查精确半径会漏掉"下一 tick 才进入半径"的邻居——那会让避让来不及
		// （实测拥挤场景下最小间距从 0.62 掉到 0.26）。
		ns = idx.Neighbors(x, z, s.refreshRadius(), self)
		s.dynIDs = s.dynIDs[:0]
		for _, n := range ns {
			s.dynIDs = append(s.dynIDs, n.Entity)
		}
		s.cache.put(self, ns)
	}

	out := s.nbBuf[:0]
	// 统一的迭代源：刷新时用查询结果，否则用缓存 id 列表。
	// 两者都只提供"有谁"，位置/半径一律实时从索引读。
	if !refresh {
		// 非刷新 tick：走缓存，且用缓存里的**句柄**直接取形状（避免往返 map 查找）。
		return s.collectFromCache(w, idx, self, x, z, out)
	}
	iterIDs := s.dynIDs
	if len(iterIDs) == 0 {
		return nil
	}
	// 保证结果按 id 有序（ORCA 的增量 LP 依赖约束顺序）。
	sortEntityIDs(iterIDs)
	for _, nid := range iterIDs {
		// **只把会自己动的实体当邻居**：ORCA 是"相互移动的物体"之间的互惠避让，
		// 对不会动的东西（树/墙/船这类没有 Moveable 的）谈"各让一半"没有意义。
		//
		// 早期版本把没有 Moveable 的也加进表里，MaxSpeed 算成 0 —— 那会在
		// ORCA 内部被当成"速度上限为 0"，产生一条退化的约束，白白干扰求解。
		if !ecs.Has[components.Moveable](w, nid) {
			continue
		}
		// 位置/半径**实时**从索引读，不用缓存值：缓存的只是"有谁"。
		// 若把位置也缓存，两个实体擦身而过时用的都是对方几个 tick 前的位置，
		// 避让会基于过期信息（那才是真正的错误）。
		nx, nz, nradius, nhalf, ok := neighborShape(idx, nid)
		if !ok {
			continue
		}
		mv := ecs.Get[components.Moveable](w, nid)
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
			X: nx, Z: nz, VX: vx, VY: vy,
			// 胶囊按外接圆处理（与 self 的半径算法一致，保证互惠对称）
			Radius: nradius + nhalf, MaxSpeed: maxSpd,
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

// collectFromCache 是**非刷新 tick** 的邻居组装：从缓存拿"有谁 + 句柄"，
// 位置/半径用句柄从索引实时读。
//
// 这样非刷新 tick 的成本 = O(邻居数) 次 slot 定位，不碰宽阶段（BVH）。
func (s *MoveSolver) collectFromCache(
	w *ecs.World, idx *collision.Index, self ecs.Entity, x, z float64, out []ORCABody,
) []ORCABody {
	cached := s.cache.get(self)
	for _, cn := range cached {
		// 只把"会自己动"的当邻居（判据 = 有 Moveable）。
		// 缓存是上一轮查出来的，期间实体可能被移除/失去 Moveable。
		if !ecs.Has[components.Moveable](w, cn.Entity) {
			continue
		}
		// 位置/半径实时取：缓存的只是"有谁"。
		nx, nz, nradius, nhalf := idx.NeighborShapeByHandle(cn.Handle)
		// 缓存是**超集**（外扩半径查的），这里按实时位置筛回精确半径，
		// 于是结果与"每 tick 精确查询"等价。
		dx, dz := nx-x, nz-z
		reach := s.neighborRadius() + nradius + nhalf
		if dx*dx+dz*dz > reach*reach {
			continue
		}
		mv := ecs.Get[components.Moveable](w, cn.Entity)
		vx, vy := mv.VelX, mv.VelY
		maxSpd := mv.EffectiveSpeed
		if maxSpd <= 0 {
			maxSpd = mv.Speed
		}
		if maxSpd <= 0 {
			maxSpd = math.Hypot(vx, vy)
		}
		out = append(out, ORCABody{
			X: nx, Z: nz, VX: vx, VY: vy,
			Radius: nradius + nhalf, MaxSpeed: maxSpd,
		})
	}
	s.nbBuf = out
	return out
}

// neighborShape 从碰撞索引实时取一个动态体的位置与形状参数。
// 索引里没有它（例如刚死亡/移除）时返回 false。
func neighborShape(idx *collision.Index, e ecs.Entity) (x, z, radius, half float64, ok bool) {
	return idx.DynamicShapeOf(e)
}

// sortEntityIDs 按 id 升序排序（ORCA 的增量 LP 依赖约束顺序，必须确定性）。
func sortEntityIDs(ids []ecs.Entity) {
	slices.Sort(ids)
}

// EnableOrcaAOI 启用 OrcaAOI（动态体专用网格）：尺寸按地图给，格子 1 格。
//
// 注意网格**只登记会自己动的实体**（有 Moveable）——这正是它与 AOI 网格的
// 关键差别：AOI 要按半径 r² 标记格子（贵），而这里每个实体只写自己那一格（O(1)），
// 查询时才读自身 r 范围的格子。代价从"标记"移到了"查询"，更适合 ORCA。
func (s *MoveSolver) EnableOrcaAOI(width, height int) {
	s.AOI = collision.NewOrcaAOI(width, height, 1)
	s.AOI.Reset()
	s.gridStamp = make(map[ecs.Entity]struct{})
	s.gridSeen = make(map[ecs.Entity]struct{})
}

// SyncOrcaAOI 在每个 tick 的移动解算**之前**调用：把动态体增量登记进 OrcaAOI。
//
// 一趟写完 ORCA 需要的**全部**字段（位置/速度/上限/形状），于是查询阶段
// 零 ECS 访问。网格只装会自己动的实体（判据 = 有 Moveable），所以查询时
// 不必再判断"有没有 Moveable"、也不必逐邻居读组件——这正是网格相对 BVH
// 的优势：BVH 是通用索引（只存形状），速度得另查。
//
// 增量：实体只在自己跨格时才动桶（同格内移动只更新缓存值）。
// 玩家 10 格/秒、tick 50ms = 每 tick 走 0.5 格 → 平均 2 tick 才跨一次格。
func (s *MoveSolver) SyncOrcaAOI(w *ecs.World) {
	if s.AOI == nil {
		return
	}
	clear(s.gridStamp)
	s.gridLive = s.gridLive[:0]
	ecs.Query2[components.Moveable, components.Position](w, func(e ecs.Entity, mv *components.Moveable, p *components.Position) {
		s.gridLive = append(s.gridLive, e)
		s.gridStamp[e] = struct{}{}

		maxSpd := mv.EffectiveSpeed
		if maxSpd <= 0 {
			maxSpd = mv.Speed
		}
		if maxSpd <= 0 {
			maxSpd = math.Hypot(mv.VelX, mv.VelY)
		}
		m := collision.Motion{
			X: float64(p.X) + mv.SubX, Z: float64(p.Y) + mv.SubY,
			VX: mv.VelX, VY: mv.VelY, MaxSpeed: maxSpd,
		}
		if ecs.Has[components.Collide](w, e) {
			col := ecs.Get[components.Collide](w, e)
			m.Radius, m.Half = col.Radius, col.HalfLength
		} else {
			m.Radius = 0.3 // 没挂 Collide 时的兜底（正常不该发生）
		}
		s.AOI.SetMotion(e, m)
	})
	// 清掉已经不存在的实体（死亡/移除/失去 Moveable）。
	for e := range s.gridSeen {
		if _, ok := s.gridStamp[e]; !ok {
			s.AOI.Remove(e)
			delete(s.gridSeen, e)
		}
	}
	for _, e := range s.gridLive {
		s.gridSeen[e] = struct{}{}
	}
}

// collectFromOrcaAOI 用 OrcaAOI 收集邻居（零 ECS 访问）。
//
// 与 collectNeighbors 的 BVH 路径相比：BVH 是通用索引（只存形状），
// 每个邻居的速度/上限都得回查 ECS；网格是为 ORCA 定制的，SyncOrcaAOI 一趟
// 把 ORCA 要的字段全缓存了，所以这里只读缓存。
func (s *MoveSolver) collectFromOrcaAOI(
	w *ecs.World, idx *collision.Index, self ecs.Entity, x, z float64, out []ORCABody,
) []ORCABody {
	// **零 ECS 访问**：网格只装会自己动的实体，且 SyncOrcaAOI 已把
	// 速度/上限/形状都缓存进去了。所以不需要 Has/Get Moveable，
	// 也不需要读 Collide —— 直接读缓存组装。
	ns := s.AOI.NeighborsFromGrid(x, z, s.neighborRadius(), self, s.gridNbBuf)
	s.gridNbBuf = ns[:0]
	for _, n := range ns {
		m, ok := s.AOI.MotionOf(n.Entity)
		if !ok {
			continue
		}
		out = append(out, ORCABody{
			X: m.X, Z: m.Z, VX: m.VX, VY: m.VY,
			Radius: m.Radius + m.Half, MaxSpeed: m.MaxSpeed,
		})
	}
	s.nbBuf = out
	return out
}

// newDefaultMoveSolver 构造缺省求解器。
//
// **缺省用 OrcaAOI**（动态体专用网格）。实测（docs/移动求解性能报告.md，
// 1000 移动体 + 2000 静态障碍）：
//
//	OrcaAOI  3.76 ms（7.5% 预算）
//	BVH     10.10 ms（20.2% 预算）   → OrcaAOI 快 63%
//
// 两者**行为逐位一致**（同一套半径语义、同样按 id 排序），所以切换零风险：
// 换的只是"怎么找候选"，不改变任何几何判定。
//
// 环境变量：
//
//	GATE_NEIGHBOR_BACKEND=bvh   退回 BVH（对照/排查用）
//	GATE_NEIGHBOR_REFRESH_TICKS 邻居表刷新间隔（缺省 1 = 不降频；实测降频是负优化）
//	GATE_ORCA_AOI_SIZE          OrcaAOI 边长（缺省 256，须 ≥ 地图尺寸）
func newDefaultMoveSolver() *MoveSolver {
	s := NewMoveSolver(NewORCASolver(DefaultORCAOptions()), 0)
	if os.Getenv("GATE_NEIGHBOR_BACKEND") == "bvh" {
		return s // 显式退回 BVH
	}
	size := envIntOr("GATE_ORCA_AOI_SIZE", 256)
	s.EnableOrcaAOI(size, size)
	return s
}
