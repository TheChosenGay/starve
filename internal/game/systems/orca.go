package systems

import (
	"math"
	"slices"
)

// Agent 是 ORCA 求解的输入：一个智能体的当前速度与期望速度。
//
// ORCA 是**速度空间**算法：在"允许速度集合"里挑一个最接近期望速度的解，
// 用它替换硬碰撞，让多个动态体互相错开而不是互相顶住。
type Agent struct {
	// X/Z 是当前位置（格）。
	X, Z float64
	// VX/VY 是**当前速度**（格/秒）。ORCA 的约束围绕当前速度构造
	//（RVO2 用 velocity_，不是 prefVelocity_，作为约束的基准点）。
	VX, VY float64
	// PrefVX/PrefVY 是期望速度（通常是阶段②滑掠后的速度）。
	PrefVX, PrefVY float64
	// Radius 是截面半径（格）。胶囊邻居按外接圆处理（半径 + 半长）。
	Radius float64
	// MaxSpeed 是本 tick 允许的最大速度（格/秒）。≤0 时用 |PrefV|。
	MaxSpeed float64
}

// ORCABody 是"别的智能体"的信息（邻居）。
type ORCABody struct {
	X, Z     float64 // 位置（格）
	VX, VY   float64 // 当前速度（格/秒）
	Radius   float64 // 半径（格）
	MaxSpeed float64
}

// ORCAOptions 是求解参数。
type ORCAOptions struct {
	// TimeHorizon 是避让的时间视野（秒）：只看这个时间内会撞上的邻居。
	// 太小 → 反应迟；太大 → 远处的人也会让你绕路。
	TimeHorizon float64
	// CollabCoeff 是互惠系数：0.5 = 双方各让一半（标准 ORCA）。
	CollabCoeff float64
	// SafetyMargin 是额外安全边距（格），吸收数值误差。
	SafetyMargin float64
}

// DefaultORCAOptions 是世界尺寸下的推荐参数。
//
// TimeHorizon（τ）= 0.5 秒：避让的"提前量"，含义是"只看 0.5 秒内会撞上的邻居"。
//
// 为什么不是更大：τ 直接决定要考察多少邻居——查询半径 ≈ 2×速度×τ。
// 玩家 10 格/秒时 τ=2s 就是 20 格半径，在 2 格间距的地图上会拉进 90+ 个邻居，
// 而绝大多数根本构不成威胁（实测 1000 实体因此要 88ms/tick，超 50ms 预算）。
// τ=0.5s 时半径 ≈10 格、邻居数降到个位数，成本随邻居数线性下降。
//
// 为什么不是更小：τ 太小会让避让显得"贴脸才躲"，观感莽撞且容易擦碰。
// 0.5 秒 = 玩家以 10 格/秒走 5 格，是"够早发现、又不过度前瞻"的折中。
func DefaultORCAOptions() ORCAOptions {
	return ORCAOptions{TimeHorizon: 0.5, CollabCoeff: 0.5, SafetyMargin: 0.02}
}

// ORCALine 是一条半平面约束，**与 RVO2 同构**：(point, direction) 定义
// "允许速度"集合 = { v | det(direction, point - v) ≤ 0 }。
//
// 用 (point, direction) 而不是 (point, normal)，是为了忠实照搬 RVO2——
// normal 形式在 leg 投影那一支极易把符号写反，进而出现"该绕开却倒退"。
type ORCALine struct {
	pointX, pointY float64
	dirX, dirY     float64
}

// det 是二维叉积（对应 RVO2 的 det）。
func det(ax, ay, bx, by float64) float64 { return ax*by - ay*bx }

// ORCASolver 是 ORCA（Optimal Reciprocal Collision Avoidance）求解器。
//
// 独立结构体，不依赖 ECS：喂进"自己 + 邻居 + 参数"，吐出"安全速度"。
// 服务端、TUI、Godot 客户端共用同一套逻辑，也能被单测直接覆盖。
//
// 实现对齐 RVO2 的 Agent::computeNewVelocity：先为每个邻居构造半平面，
// 再用增量式二维线性规划求最接近期望速度的可行点。
type ORCASolver struct {
	Opts ORCAOptions
	// breakSymmetry 打开对称打破（正面对撞时不再双双停死）。
	breakSymmetry bool
	// symmetryKey 是"我是谁"的稳定标识（实体 id），决定往哪一侧让。
	symmetryKey uint64

	// buf/lines 是复用的临时缓冲：Solve 在每个 tick 被每个移动实体调用一次，
	// 每次新分配切片会让 GC 压力很大（实测 1000 实体/tick 分配 127MB）。
	// 求解器本身不是并发安全的——服务端 MoveSystem 是单线程顺序调用，
	// 客户端各自持有自己的实例，所以复用是安全的。
	buf   []ORCABody
	lines []ORCALine
}

// NewORCASolver 建一个求解器（零值参数用默认值）。
func NewORCASolver(opts ORCAOptions) *ORCASolver {
	if opts.TimeHorizon <= 0 {
		opts.TimeHorizon = DefaultORCAOptions().TimeHorizon
	}
	if opts.CollabCoeff <= 0 {
		opts.CollabCoeff = DefaultORCAOptions().CollabCoeff
	}
	if opts.SafetyMargin < 0 {
		opts.SafetyMargin = DefaultORCAOptions().SafetyMargin
	}
	return &ORCASolver{Opts: opts}
}

// NewORCASolverFor 建一个带对称打破的求解器：key 是调用方的稳定身份
// （服务端/客户端都应传实体 id），保证同一对实体总是各自往固定一侧让。
func NewORCASolverFor(opts ORCAOptions, key uint64) *ORCASolver {
	s := NewORCASolver(opts)
	s.breakSymmetry = true
	s.symmetryKey = key
	return s
}

// Solve 返回避让后的安全速度。邻居顺序不影响结果（内部固定排序）。
// 返回速度方向可任意（非 8 向），模长 ≤ maxSpeed。
func (s *ORCASolver) Solve(self Agent, neighbors []ORCABody) (float64, float64) {
	maxSpeed := self.MaxSpeed
	if maxSpeed <= 0 {
		maxSpeed = math.Hypot(self.PrefVX, self.PrefVY)
	}
	if maxSpeed <= 0 {
		return 0, 0
	}
	prefX, prefY := clampLen(self.PrefVX, self.PrefVY, maxSpeed)
	if len(neighbors) == 0 {
		return prefX, prefY
	}

	// 注意：对称打破**不在这里**做。曾经的写法是给期望速度加 3% 的垂直扰动，
	// 但那样会让"空旷/并行/单邻居"等所有情形都被平白扰动（客户端若不做同样扰动
	// 就会与服务端分叉）。现在只在真正需要的地方做：agentLine 里选 leg 时的
	// det 偏置（见下方 side 的注释）——那才是"正面对撞左右分侧"的决策点。

	// 邻居排序：LP 是增量的，约束加入顺序会影响退化情形的解，
	// 必须固定顺序才能让服务端/客户端复现同一结果。
	//
	// 用 slices.SortFunc 而不是 sort.Slice：后者走反射，在每实体每次求解的热路径上
	// 开销可观（实测占 Solve 的 ~15%）。缓冲复用避免每次求解都分配。
	sorted := s.buf[:0]
	sorted = append(sorted, neighbors...)
	slices.SortFunc(sorted, func(a, b ORCABody) int {
		if a.X != b.X {
			return cmpFloat(a.X, b.X)
		}
		if a.Z != b.Z {
			return cmpFloat(a.Z, b.Z)
		}
		return cmpFloat(a.Radius, b.Radius)
	})
	s.buf = sorted

	lines := s.lines[:0]
	for _, n := range sorted {
		if line, ok := s.agentLine(self, n); ok {
			lines = append(lines, line)
		}
	}
	s.lines = lines
	if len(lines) == 0 {
		return prefX, prefY
	}
	return linearProgram2(lines, maxSpeed, prefX, prefY)
}

// cmpFloat 是三路比较（返回 -1/0/1），避免 sort 回调里写两遍条件。
func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// agentLine 为邻居 n 构造 ORCA 半平面（对齐 RVO2）。
// 返回 false 表示"这个邻居不构成威胁"。
//
// 关键量与符号约定（照搬 RVO2）：
//
//	relativePosition = n.pos - self.pos
//	relativeVelocity = self.vel - n.vel      （self-other）
//	w = relativeVelocity - invTimeHorizon * relativePosition
//
// 两种投影：
//   - dotProduct1 < 0 且 dotProduct1² > R²·|w|² → 投影到 cutoff **圆**（正面来撞）；
//   - 否则投影到两条 **leg**（速度障碍锥的边）→ 这才是"从旁边滑过去"的解。
func (s *ORCASolver) agentLine(self Agent, n ORCABody) (ORCALine, bool) {
	invTau := 1 / s.Opts.TimeHorizon
	relX := n.X - self.X
	relZ := n.Z - self.Z
	relVX := self.VX - n.VX
	relVZ := self.VY - n.VY
	combined := self.Radius + n.Radius + s.Opts.SafetyMargin
	combinedSq := combined * combined
	distSq := relX*relX + relZ*relZ

	var ux, uz, dirX, dirY float64

	if distSq > combinedSq {
		wX := relVX - invTau*relX
		wZ := relVZ - invTau*relZ
		wLenSq := wX*wX + wZ*wZ
		dot1 := wX*relX + wZ*relZ

		if dot1 < 0 && dot1*dot1 > combinedSq*wLenSq {
			// 投影到 cutoff 圆（正面对撞）。
			wLen := math.Sqrt(wLenSq)
			if wLen <= 1e-12 {
				return ORCALine{}, false
			}
			unitWX, unitWZ := wX/wLen, wZ/wLen
			dirX, dirY = unitWZ, -unitWX
			ux, uz = (combined*invTau-wLen)*unitWX, (combined*invTau-wLen)*unitWZ
		} else {
			// 投影到 leg（速度障碍锥的边）：给出"擦过去"的解。
			// 两条 leg 分居连心线两侧，选哪条决定"从左过还是从右过"。
			//
			// 对称打破：正面对撞时 det(relPos, w) 恰好为 0（完全共线），
			// 两个人会选中同一条 leg → 往同一侧让 → 仍然撞上/停死。
			// 这里按实体的稳定身份在 det 上加一个极小偏置，让一方选左腿、
			// 另一方选右腿。偏置量级远小于正常 det，不影响非对称情形。
			side := det(relX, relZ, wX, wZ)
			if s.breakSymmetry {
				if s.symmetryKey%2 == 0 {
					side -= 1e-9
				} else {
					side += 1e-9
				}
			}
			leg := math.Sqrt(distSq - combinedSq)
			if side > 0 {
				dirX = (relX*leg - relZ*combined) / distSq
				dirY = (relX*combined + relZ*leg) / distSq
			} else {
				dirX = -(relX*leg + relZ*combined) / distSq
				dirY = -(-relX*combined + relZ*leg) / distSq
			}
			dot2 := relVX*dirX + relVZ*dirY
			ux, uz = dot2*dirX-relVX, dot2*dirY-relVZ
		}
	} else {
		// 已重叠：用位置连心线方向，把重叠量折成速度约束。
		d := math.Sqrt(distSq)
		if d <= 1e-12 {
			// 完全重合：挑一个固定方向分开，保证输出有限且确定。
			return ORCALine{
				pointX: self.VX + combined*invTau*0.5,
				pointY: self.VY,
				dirX:   0, dirY: 1,
			}, true
		}
		ux, uz = relX/d, relZ/d
		dirX, dirY = uz, -ux
		shortfall := combined*invTau - (-(relVX*ux + relVZ*uz))
		ux, uz = ux*shortfall, uz*shortfall
	}

	dirLen := math.Hypot(dirX, dirY)
	if dirLen <= 1e-12 {
		return ORCALine{}, false
	}
	dirX, dirY = dirX/dirLen, dirY/dirLen

	// point = 当前速度 + 互惠系数 * u
	k := s.Opts.CollabCoeff
	return ORCALine{
		pointX: self.VX + k*ux,
		pointY: self.VY + k*uz,
		dirX:   dirX,
		dirY:   dirY,
	}, true
}

// linearProgram2 在若干半平面约束下，求最接近 (prefX,prefY) 的可行点，
// 并夹在半径 maxSpeed 的圆内（对齐 RVO2 的 linearProgram2）。
func linearProgram2(lines []ORCALine, maxSpeed, prefX, prefY float64) (float64, float64) {
	bestX, bestY := clampLen(prefX, prefY, maxSpeed)
	for i := range lines {
		if det(lines[i].dirX, lines[i].dirY, lines[i].pointX-bestX, lines[i].pointY-bestY) <= 0 {
			continue // 满足该约束
		}
		nx, ny, ok := linearProgram1(lines, i, maxSpeed, prefX, prefY)
		if !ok {
			// 该约束与速度圆不相交：退化为沿约束边界投影，保证输出有限。
			nx, ny = projectOntoLine(lines[i], bestX, bestY)
			nx, ny = clampLen(nx, ny, maxSpeed)
		}
		bestX, bestY = nx, ny
	}
	return bestX, bestY
}

// linearProgram1 在第 lineNo 条约束的**边界直线**上求最接近 optVelocity、
// 且满足前 lineNo 条约束并在速度圆内的点（对齐 RVO2 的 linearProgram1）。
func linearProgram1(lines []ORCALine, lineNo int, radius, optX, optY float64) (float64, float64, bool) {
	l := lines[lineNo]
	dotProduct := l.pointX*l.dirX + l.pointY*l.dirY
	disc := dotProduct*dotProduct + radius*radius - (l.pointX*l.pointX + l.pointY*l.pointY)
	if disc < 0 {
		return 0, 0, false // 速度圆与该直线不相交
	}
	sqrtDisc := math.Sqrt(disc)
	tLeft := -dotProduct - sqrtDisc
	tRight := -dotProduct + sqrtDisc

	for i := 0; i < lineNo; i++ {
		li := lines[i]
		denom := det(l.dirX, l.dirY, li.dirX, li.dirY)
		num := det(li.dirX, li.dirY, l.pointX-li.pointX, l.pointY-li.pointY)
		if math.Abs(denom) <= 1e-12 {
			if num < 0 {
				return 0, 0, false // 平行且不可行
			}
			continue
		}
		t := num / denom
		if denom >= 0 {
			if t < tRight {
				tRight = t
			}
		} else {
			if t > tLeft {
				tLeft = t
			}
		}
		if tLeft > tRight {
			return 0, 0, false
		}
	}
	t := l.dirX*(optX-l.pointX) + l.dirY*(optY-l.pointY)
	if t < tLeft {
		t = tLeft
	} else if t > tRight {
		t = tRight
	}
	return l.pointX + t*l.dirX, l.pointY + t*l.dirY, true
}

// projectOntoLine 把点投到约束边界上（兜底用）。
func projectOntoLine(l ORCALine, x, y float64) (float64, float64) {
	d2 := l.dirX*l.dirX + l.dirY*l.dirY
	if d2 <= 1e-12 {
		return x, y
	}
	px, py := x-l.pointX, y-l.pointY
	proj := (px*l.dirX + py*l.dirY) / d2
	return l.pointX + proj*l.dirX, l.pointY + proj*l.dirY
}

// clampLen 把向量截断到不超过 maxLen（maxLen ≤ 0 时返回零向量）。
func clampLen(x, y, maxLen float64) (float64, float64) {
	l := math.Hypot(x, y)
	if l <= maxLen || l <= 1e-12 {
		return x, y
	}
	return x / l * maxLen, y / l * maxLen
}
