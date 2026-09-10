// 本文件是宽阶段（扫描器 + 引擎）两个浏览器场景的“纯逻辑”部分，
// 不引用 syscall/js，因此可以在宿主机上编译和单测。浏览器胶水在 main_wasm.go。
//
//	场景 1（broad.html）：同一批物体、同样的查询——对比“不用扫描器”与“用扫描器”。
//	场景 2（swing.html）：挥砍扇形判定，返回命中部位供前端画局部标记。
package main

import (
	"math"
	"time"

	"starve/pkg/collide"
)

// nowMs 返回单调时钟的毫秒数（宿主与 wasm 都能用；
// 浏览器端的墙钟耗时由 JS 侧的 performance.now 负责，这里用来在后端内部对拍）。
func nowMs() float64 { return float64(time.Now().UnixNano()) / 1e6 }

// splitmix64 是确定性哈希：查询位置必须与模式无关，所以不能用有状态的 RNG。
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	x = (x ^ (x >> 30)) * 0xBF58476D1CE4E5B9
	x = (x ^ (x >> 27)) * 0x94D049BB133111EB
	return x ^ (x >> 31)
}

func rand01(seed int64, a, b int) float64 {
	h := splitmix64(uint64(seed)*0x9E3779B97F4A7C15 ^ uint64(a)*0xD1B54A32D192ED03 ^ uint64(b)*0x8CB92BA72F3D8DD7)
	return float64(h>>11) / float64(1<<53)
}

// ---- 场景 1：宽阶段压测 ----

type bpSetupIn struct {
	Count  int     `json:"count"`
	Seed   int64   `json:"seed"`
	Bound  float64 `json:"bound"`  // 广场半边长
	Margin float64 `json:"margin"` // fat AABB 外扩量
	Radius float64 `json:"radius"` // 物体半径
	Height float64 `json:"height"` // 物体高度
}

type bpSetupOut struct {
	OK    bool  `json:"ok"`
	Count int   `json:"count"`
	Seed  int64 `json:"seed"`
}

type bpStepIn struct {
	Dt      float64 `json:"dt"`
	Speed   float64 `json:"speed"`
	Dirty   float64 `json:"dirty"`   // 本帧移动的物体比例（0..1）
	Rebuild bool    `json:"rebuild"` // true = 每帧全量重建索引
}

type bpStepOut struct {
	Positions []vec3j   `json:"positions"`
	Radii     []float64 `json:"radii"`
	Bounced   int       `json:"bounced"`
}

type bpQueryIn struct {
	Mode   string  `json:"mode"` // "naive" | "scanner"
	Count  int     `json:"count"`
	Radius float64 `json:"radius"`
}

type bpQueryOut struct {
	Mode       string  `json:"mode"`
	Candidates int     `json:"candidates"` // 宽阶段给出的候选数（naive 时等于全部）
	Hits       int     `json:"hits"`
	Narrow     int     `json:"narrow"` // 窄阶段调用次数
	Pairs      float64 `json:"pairs"`  // 平均每次查询的窄阶段调用次数
	Ms         float64 `json:"ms"`     // 这一批查询的总耗时（毫秒）
	Points     []vec3j `json:"points"` // 命中点（前若干个，用于可视化）
	Flags      string  `json:"flags"`  // 每个物体是否为首个查询的候选（'1'/'0'，物体太多时为空）
	QX         float64 `json:"qx"`     // 首个查询球
	QZ         float64 `json:"qz"`
	QR         float64 `json:"qr"`
}

// bpState 是压测场景的持久状态：引擎与物体列表在多次调用之间复用，
// 这样“增量更新”才是真的增量（每次调用都重建引擎就体现不出差异了）。
type bpState struct {
	engine  *collide.Engine
	handles []collide.Handle
	bodies  []collide.Capsule
	vel     []collide.Vec3
	radius  float64
	height  float64
	bound   float64
	seed    int64
	tick    int
}

var bpWorld bpState

// bpSetup 建立场景：count 个胶囊，确定性散布在 [-bound, bound]² 内。
func bpSetup(in bpSetupIn) bpSetupOut {
	n := in.Count
	if n < 0 {
		n = 0
	}
	bound := in.Bound
	if bound <= 0 {
		bound = 30
	}
	radius := in.Radius
	if radius <= 0 {
		radius = 0.4
	}
	height := in.Height
	if height <= 0 {
		height = 1.8
	}

	st := bpState{
		engine:  collide.NewEngine(collide.EngineOptions{Margin: in.Margin}),
		handles: make([]collide.Handle, 0, n),
		bodies:  make([]collide.Capsule, 0, n),
		vel:     make([]collide.Vec3, 0, n),
		radius:  radius,
		height:  height,
		bound:   bound,
		seed:    in.Seed,
	}
	st.engine.Reserve(n)
	for i := 0; i < n; i++ {
		x := (rand01(in.Seed, i, 0)*2 - 1) * bound
		z := (rand01(in.Seed, i, 1)*2 - 1) * bound
		body := collide.Capsule{
			A: collide.Vec3{X: x, Z: z},
			B: collide.Vec3{X: x, Y: height, Z: z},
			R: radius,
		}
		st.bodies = append(st.bodies, body)
		st.handles = append(st.handles, st.engine.Add(body))
		// 速度方向随机：让物体互相穿插、密度处处不同，查询代价才有代表性。
		ang := rand01(in.Seed, i, 2) * 2 * math.Pi
		st.vel = append(st.vel, collide.Vec3{X: math.Cos(ang), Z: math.Sin(ang)})
	}
	bpWorld = st
	return bpSetupOut{OK: true, Count: n, Seed: in.Seed}
}

// bpStep 推进一帧：移动物体并同步索引（增量更新，或全量重建）。
func bpStep(in bpStepIn) bpStepOut {
	st := &bpWorld
	n := len(st.bodies)
	out := bpStepOut{Positions: make([]vec3j, n), Radii: make([]float64, n)}
	if n == 0 {
		return out
	}
	dt := in.Dt
	if dt <= 0 {
		dt = 1.0 / 60.0
	}
	speed := in.Speed
	if speed <= 0 {
		speed = 6
	}
	dirty := in.Dirty
	if dirty <= 0 || dirty > 1 {
		dirty = 1
	}
	st.tick++

	// 只有一部分物体移动：真实的服务器 tick 里，绝大多数实体是静止的。
	moving := int(float64(n) * dirty)
	for i := 0; i < n; i++ {
		if i < moving {
			p := st.bodies[i].A.Add(st.vel[i].Scale(speed * dt))
			if p.X > st.bound || p.X < -st.bound {
				st.vel[i].X = -st.vel[i].X
				p.X = clampF(p.X, -st.bound, st.bound)
				out.Bounced++
			}
			if p.Z > st.bound || p.Z < -st.bound {
				st.vel[i].Z = -st.vel[i].Z
				p.Z = clampF(p.Z, -st.bound, st.bound)
				out.Bounced++
			}
			st.bodies[i].A.X, st.bodies[i].A.Z = p.X, p.Z
			st.bodies[i].B.X, st.bodies[i].B.Z = p.X, p.Z
		}
		out.Positions[i] = jv(st.bodies[i].A)
		out.Radii[i] = st.bodies[i].R
	}

	if in.Rebuild {
		st.engine.Rebuild() // 全量重建：模拟“每帧重建索引”的做法
		return out
	}
	for i := 0; i < moving; i++ {
		st.engine.Update(st.handles[i], st.bodies[i])
	}
	return out
}

// bpQueries 生成这一帧的查询球：位置只依赖 (seed, tick, i)，
// 所以两种模式拿到的是同一批查询，对比才公平。
func bpQueries(st *bpState, count int, radius float64) []collide.Sphere {
	out := make([]collide.Sphere, count)
	for i := 0; i < count; i++ {
		x := (rand01(st.seed, st.tick, 1000+i*2)*2 - 1) * st.bound
		z := (rand01(st.seed, st.tick, 1001+i*2)*2 - 1) * st.bound
		out[i] = collide.Sphere{C: collide.Vec3{X: x, Y: st.height * 0.5, Z: z}, R: radius}
	}
	return out
}

// bpQuery 跑一批查询：
//   - naive：不用扫描器——每次查询直接把全部物体喂给窄阶段；
//   - scanner：扫描器先做 AABB 剔除，只有候选进窄阶段。
//
// 两者命中的目标必须一致（差分测试覆盖），差别只在窄阶段调用次数与耗时。
func bpQuery(in bpQueryIn) bpQueryOut {
	st := &bpWorld
	out := bpQueryOut{Mode: in.Mode, Points: []vec3j{}}
	if len(st.bodies) == 0 {
		return out
	}
	count := in.Count
	if count <= 0 {
		count = 1
	}
	radius := in.Radius
	if radius <= 0 {
		radius = 1.6
	}
	queries := bpQueries(st, count, radius)
	out.QX, out.QZ, out.QR = queries[0].C.X, queries[0].C.Z, queries[0].R

	// 首个查询的候选标记：物体不多时才回传（避免大场景每帧传几十 KB）。
	flag := len(st.bodies) <= 3000
	var flags []byte
	if flag {
		flags = make([]byte, len(st.bodies))
		for i := range flags {
			flags[i] = '0'
		}
	}
	var idxOf map[collide.Handle]int
	if flag {
		idxOf = make(map[collide.Handle]int, len(st.handles))
		for i, h := range st.handles {
			idxOf[h] = i
		}
	}

	start := nowMs()
	if in.Mode == "scanner" {
		for qi, q := range queries {
			// 扫描器先按 AABB 剔除，只有候选进窄阶段。
			st.engine.Query(q.Bounds(), func(h collide.Handle) bool {
				out.Candidates++
				if flag && qi == 0 {
					flags[idxOf[h]] = '1'
				}
				out.Narrow++
				body, ok := st.engine.Shape(h).(collide.Capsule)
				if !ok || !collide.TestShapes(q, body) {
					return true
				}
				out.Hits++
				if len(out.Points) < 256 {
					out.Points = append(out.Points, jv(collide.ClosestSurfacePoint(q.C, body)))
				}
				return true
			})
		}
	} else {
		for qi, q := range queries {
			for i := range st.bodies {
				if flag && qi == 0 {
					flags[i] = '1'
				}
				out.Narrow++
				if !collide.TestShapes(q, st.bodies[i]) {
					continue
				}
				out.Hits++
				if len(out.Points) < 256 {
					out.Points = append(out.Points, jv(collide.ClosestSurfacePoint(q.C, st.bodies[i])))
				}
			}
		}
		out.Candidates = out.Narrow
	}
	out.Ms = nowMs() - start
	if count > 0 {
		out.Pairs = float64(out.Narrow) / float64(count)
	}
	if flag {
		out.Flags = string(flags)
	}
	return out
}

// ---- 场景 2：挥砍扇形 ----

type sectorBody struct {
	X      float64 `json:"x"`
	Z      float64 `json:"z"`
	Radius float64 `json:"radius"`
	Height float64 `json:"height"`
}

type sectorSetupIn struct {
	Player  sectorBody   `json:"player"`
	Targets []sectorBody `json:"targets"`
}

type sectorStepIn struct {
	From float64 `json:"from"`
	To   float64 `json:"to"`
	R0   float64 `json:"r0"`
	R1   float64 `json:"r1"`
}

type sectorHit struct {
	Index  int     `json:"index"`
	Point  vec3j   `json:"point"`
	Normal vec3j   `json:"normal"`
	Dist   float64 `json:"dist"`
}

type sectorStepOut struct {
	Candidates int         `json:"candidates"`
	Hits       []sectorHit `json:"hits"`
	Ms         float64     `json:"ms"`
}

type sectorState struct {
	engine  *collide.Engine
	player  collide.Handle
	targets []collide.Handle
	center  collide.Vec3
}

var sectorWorld sectorState

// sectorSetup 建立挥砍场景：一个玩家 + 若干目标，全部注册进引擎。
// 注册只发生这一次——这就是“业务只在创建时声明图元”的那一步。
func sectorSetup(in sectorSetupIn) bool {
	eng := collide.NewEngine(collide.EngineOptions{Margin: 0.2})
	eng.Reserve(len(in.Targets) + 1)
	st := sectorState{engine: eng}
	st.center = collide.Vec3{X: in.Player.X, Z: in.Player.Z}
	st.player = eng.Add(capsuleOf(in.Player))
	for _, t := range in.Targets {
		st.targets = append(st.targets, eng.Add(capsuleOf(t)))
	}
	sectorWorld = st
	return true
}

func capsuleOf(b sectorBody) collide.Capsule {
	h := b.Height
	if h <= 0 {
		h = 1.8
	}
	r := b.Radius
	if r <= 0 {
		r = 0.4
	}
	return collide.Capsule{
		A: collide.Vec3{X: b.X, Z: b.Z},
		B: collide.Vec3{X: b.X, Y: h, Z: b.Z},
		R: r,
	}
}

// sectorStep 用扇形查询当前挥砍范围，返回每个被命中目标的命中部位。
// 前端拿 Point 在目标身上画局部红色——这正是“引擎返回碰撞部位”的用处。
func sectorStep(in sectorStepIn) sectorStepOut {
	st := &sectorWorld
	out := sectorStepOut{Hits: []sectorHit{}}
	if st.engine == nil {
		return out
	}
	r0, r1 := in.R0, in.R1
	if r1 <= r0 {
		r1 = r0 + 0.1
	}
	s := collide.Sector{Center: st.center, From: in.From, To: in.To, R0: r0, R1: r1}

	start := nowMs()
	// 候选数：先做一次纯宽阶段查询，展示“宽阶段剔除了多少”。
	st.engine.Query(s.Bounds(), func(collide.Handle) bool {
		out.Candidates++
		return true
	})
	idx := make(map[collide.Handle]int, len(st.targets))
	for i, h := range st.targets {
		idx[h] = i
	}
	st.engine.OverlapSector(s, func(h collide.Handle) bool { return h != st.player },
		func(r collide.Result) bool {
			out.Hits = append(out.Hits, sectorHit{
				Index:  idx[r.Handle],
				Point:  jv(r.Point),
				Normal: jv(r.Normal),
				Dist:   r.Dist,
			})
			return true
		})
	out.Ms = nowMs() - start
	return out
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
