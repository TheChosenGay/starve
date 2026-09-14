// 本文件是「丢炸弹」场景的纯逻辑部分（不引用 syscall/js，宿主机可单测）。
//
// 一次爆炸 = 一次**球体积查询**：
//
//	鼠标 → 屏幕射线 → 地面落点（IntersectRayAABB 打一块厚地面板）
//	落点 + 半径 → Engine.Overlap(Sphere) → 半径内的胶囊全部进回调
//	每个目标按「爆心到它表面的距离」算强度 → 闪红 + 沿背离爆心的方向击退
//
// 与挥砍（扇形）同源：前端只给数字（射线、半径、引信、力度），几何全由 pkg/collide 算。
// 这一页要回答的是「一块几何决定一批结果」：同一个爆心，谁闪红、闪多红、被推多远，
// 都由那一次 Sphere 查询的结果推出来。
package main

import (
	"math"

	"starve/pkg/collide"
)

// 场景常量
const (
	blastTargetRadius  = 0.35
	blastTargetHeight  = 1.7
	blastBombY         = 0.35 // 爆心离地高度（炸弹躺在地上，球心略高于地面）
	blastGroundSlab    = 2.0  // 鼠标拾取用地面板厚度
	blastMaxRay        = 1e4  // 射线最长距离（前端不传时兜底）
	blastGroundHalf    = 1e4  // 拾取地面板的半边长度
	blastDefaultRadius = 4.0
	blastDefaultFuse   = 0.7
	blastDefaultPower  = 7.0  // 击退初速（米/秒），按强度缩放
	blastFlashDecay    = 1.6  // 闪红衰减（每秒）
	blastPushDeadzone  = 0.1  // 目标中心离爆心这么近时，"背离爆心"没有确定方向，改用投掷方向
	blastGravity       = 16.0 // 炸飞后的重力（米/秒²）
	blastLift          = 8.5  // 起飞初速（米/秒，满强度 ≈ 1.8 米高，见 blastGravity）
	blastLiftBase      = 0.45 // 抬升里与接触点高度无关的那部分（保证任何命中都会离地）
	blastSpin          = 6.0  // 翻滚角速度（弧度/秒，满强度）
	blastSpinBase      = 0.5
	blastAirDrag       = 0.7  // 空中水平阻尼（1/秒）
	blastRestitution   = 0.28 // 落地回弹系数
	blastBounceFric    = 0.55 // 落地时水平速度保留比例
	blastBounceMin     = 1.0  // 落地竖直速度小于它就趴下，不再弹
	blastSpinDecay     = 3.5  // 落地后翻滚回正速度（1/秒）
)

// blastBody 是场景里一个目标（胶囊）的初始状态。
type blastBody struct {
	X      float64 `json:"x"`
	Z      float64 `json:"z"`
	Radius float64 `json:"radius"`
	Height float64 `json:"height"`
	Dir    float64 `json:"dir"`   // 漫游方向（弧度）
	Speed  float64 `json:"speed"` // 漫游速度倍率
}

type blastSetupIn struct {
	Thrower blastBody   `json:"thrower"`
	Targets []blastBody `json:"targets"`
	Arena   float64     `json:"arena"` // 场地半边长
}

// blastRayIn 是「鼠标射线」：屏幕坐标已在前端换算成世界空间的一条射线
// （正交相机，所以 origin = 屏幕点在世界空间的落位 + 视线方向上的远置偏移）。
type blastRayIn struct {
	Origin vec3j   `json:"origin"`
	Dir    vec3j   `json:"dir"`
	Max    float64 `json:"max"`
	Radius float64 `json:"radius"`
}

type blastAimOut struct {
	Ok         bool    `json:"ok"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Z          float64 `json:"z"`
	Candidates int     `json:"candidates"` // 半径内进了宽阶段的候选数
	Ms         float64 `json:"ms"`
}

type blastThrowIn struct {
	Origin vec3j   `json:"origin"`
	Dir    vec3j   `json:"dir"`
	Max    float64 `json:"max"`
	Radius float64 `json:"radius"`
	Fuse   float64 `json:"fuse"`
	Power  float64 `json:"power"`
}

type blastThrowOut struct {
	Ok     bool    `json:"ok"`
	Index  int     `json:"index"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Z      float64 `json:"z"`
	Radius float64 `json:"radius"`
	Fuse   float64 `json:"fuse"`
}

type blastStepIn struct {
	Dt      float64 `json:"dt"`
	Speed   float64 `json:"speed"`   // 目标漫游速度（米/秒）
	Damping float64 `json:"damping"` // 击退速度衰减（1/秒）
}

// blastBombOut 是还没炸的炸弹：落点 + 剩余引信（前端拿它画抛物线落点与引信环）。
type blastBombOut struct {
	X      float64 `json:"x"`
	Z      float64 `json:"z"`
	Radius float64 `json:"radius"`
	Fuse   float64 `json:"fuse"`
	Total  float64 `json:"total"`
}

type blastHitOut struct {
	Index     int     `json:"index"`
	Depth     float64 `json:"depth"`     // 接触穿透深度（碰撞查询给的能量刻度）
	Intensity float64 `json:"intensity"` // 0..1 = min(Depth / 半径, 1)
	Speed     float64 `json:"speed"`     // 本次击飞的初速（米/秒）
	Point     vec3j   `json:"point"`     // 接触点（爆炸球与胶囊接触区域的代表点）
	Normal    vec3j   `json:"normal"`    // 击飞方向（水平单位向量）
	Lift      float64 `json:"lift"`      // 起飞竖直初速（米/秒）
}

type blastBoomOut struct {
	X          float64       `json:"x"`
	Z          float64       `json:"z"`
	Radius     float64       `json:"radius"`
	Hits       []blastHitOut `json:"hits"`
	Candidates int           `json:"candidates"`
	Ms         float64       `json:"ms"`
}

type blastStepOut struct {
	X          []float64      `json:"x"`
	Z          []float64      `json:"z"`
	Y          []float64      `json:"y"`     // 离地高度（被炸飞时 > 0）
	Flash      []float64      `json:"flash"` // 每个目标的闪红强度 0..1
	Spin       []float64      `json:"spin"`  // 翻滚角（弧度）
	RollX      []float64      `json:"rollX"` // 翻滚轴 = 竖直 × 击飞方向（水平单位向量）
	RollZ      []float64      `json:"rollZ"`
	Bombs      []blastBombOut `json:"bombs"`
	Booms      []blastBoomOut `json:"booms"` // 本帧新爆的（前端拿它放冲击波）
	Explosions int            `json:"explosions"`
	HitTotal   int            `json:"hitTotal"`
	Ms         float64        `json:"ms"` // 爆炸查询耗时（引擎内，与其它页同一口径）
}

type blastAgent struct {
	x, z   float64
	dx, dz float64 // 单位方向
	speed  float64
	radius float64
	height float64
	vx, vz float64 // 击退速度（米/秒），按 Damping 衰减
	flash  float64 // 闪红强度，随时间衰减
	y      float64 // 离地高度：被炸飞时 > 0，落地回到 0
	vy     float64 // 竖直速度（米/秒）
	spin   float64 // 翻滚角（弧度）
	spinV  float64 // 翻滚角速度
	rollX  float64 // 翻滚轴（水平单位向量，击飞时按击飞方向设定）
	rollZ  float64
}

type blastBomb struct {
	x, z                float64
	radius, fuse, total float64
	power               float64
	fromX, fromZ        float64 // 投掷手的水平位置：贴脸炸时的兜底击退方向
}

type blastState struct {
	engine     *collide.Engine
	handles    []collide.Handle
	index      map[collide.Handle]int
	agents     []blastAgent
	bombs      []blastBomb
	arena      float64
	throwerX   float64
	throwerZ   float64
	tick       int
	explosions int
	hitTotal   int
}

var blastWorld blastState

// blastSetup 建立场景：一个投掷手（只用于渲染抛物线的起点）+ 若干漫游目标，
// 全部注册进引擎（只做一次）。
func blastSetup(in blastSetupIn) bool {
	eng := collide.NewEngine(collide.EngineOptions{Margin: 0.4})
	eng.Reserve(len(in.Targets))
	st := blastState{
		engine:   eng,
		index:    make(map[collide.Handle]int, len(in.Targets)),
		arena:    in.Arena,
		throwerX: in.Thrower.X,
		throwerZ: in.Thrower.Z,
	}
	if st.arena <= 0 {
		st.arena = 9
	}
	for _, t := range in.Targets {
		r, h := blastShapeOf(t)
		hnd := eng.Add(blastCapsule(t.X, t.Z, 0, r, h))
		st.index[hnd] = len(st.agents)
		st.handles = append(st.handles, hnd)
		sp := t.Speed
		if sp <= 0 {
			sp = 1
		}
		st.agents = append(st.agents, blastAgent{
			x: t.X, z: t.Z,
			dx:     math.Cos(t.Dir),
			dz:     math.Sin(t.Dir),
			speed:  sp,
			radius: r,
			height: h,
		})
	}
	blastWorld = st
	return true
}

// blastShapeOf 取一个目标的有效尺寸（缺省 0.35 × 1.7）。
func blastShapeOf(b blastBody) (radius, height float64) {
	radius, height = b.Radius, b.Height
	if radius <= 0 {
		radius = blastTargetRadius
	}
	if height <= 0 {
		height = blastTargetHeight
	}
	return radius, height
}

// blastCapsule 把一个目标变成胶囊图元；y 是底端离地高度（被炸飞时 > 0）。
// 图元跟着高度走，所以飞在空中的目标真的更难被下一颗地面炸弹炸到——
// 高度不是渲染骗人，而是回到了碰撞查询里。
func blastCapsule(x, z, y, radius, height float64) collide.Capsule {
	return collide.Capsule{
		A: collide.Vec3{X: x, Y: y, Z: z},
		B: collide.Vec3{X: x, Y: y + height, Z: z},
		R: radius,
	}
}

// blastGroundHit 把一条射线打到地面上。
//
// 地面不是"数学平面"而是一块厚板——这样这一步也能直接用库里的 IntersectRayAABB，
// 前端不必自己写平面求交；射线打不到地面（抬头看天）时如实返回 false。
func blastGroundHit(o, d collide.Vec3, maxDist float64) (collide.Vec3, bool) {
	slab := collide.AABB{
		Min: collide.Vec3{X: -blastGroundHalf, Y: -blastGroundSlab, Z: -blastGroundHalf},
		Max: collide.Vec3{X: blastGroundHalf, Y: 0, Z: blastGroundHalf},
	}
	hit := collide.IntersectRayAABB(o, d, maxDist, slab)
	if !hit.Hit {
		return collide.Vec3{}, false
	}
	return hit.Point, true
}

// blastLandPoint 由鼠标射线求落点：命中地面后夹进场地内，
// 于是"点到场地外面"的炸弹落在边缘，而不是画到看不见的地方。
func blastLandPoint(st *blastState, in blastRayIn) (collide.Vec3, bool) {
	max := in.Max
	if max <= 0 {
		max = blastMaxRay
	}
	p, ok := blastGroundHit(v3(in.Origin), v3(in.Dir).Normalized(), max)
	if !ok {
		return collide.Vec3{}, false
	}
	p.X = clampF(p.X, -st.arena, st.arena)
	p.Z = clampF(p.Z, -st.arena, st.arena)
	return p, true
}

// blastQuery 以地面点 p 为心、r 为半径构造爆炸球（爆心略高于地面）。
func blastQuery(p collide.Vec3, r float64) collide.Sphere {
	return collide.Sphere{C: collide.Vec3{X: p.X, Y: blastBombY, Z: p.Z}, R: r}
}

// blastAim 只做瞄准：把鼠标射线落到地面上，并顺手报出半径内的宽阶段候选数。
func blastAim(in blastRayIn) blastAimOut {
	st := &blastWorld
	out := blastAimOut{}
	if st.engine == nil {
		return out
	}
	start := nowMs()
	p, ok := blastLandPoint(st, in)
	if !ok {
		out.Ms = nowMs() - start
		return out
	}
	out.Ok, out.X, out.Y, out.Z = true, p.X, p.Y, p.Z
	if in.Radius > 0 {
		q := blastQuery(p, in.Radius)
		st.engine.Query(q.Bounds(), func(collide.Handle) bool {
			out.Candidates++
			return true
		})
	}
	out.Ms = nowMs() - start
	return out
}

// blastThrow 丢一颗炸弹：落点同样由射线决定，之后每帧按引信倒计时，到点爆炸。
func blastThrow(in blastThrowIn) blastThrowOut {
	st := &blastWorld
	out := blastThrowOut{}
	if st.engine == nil {
		return out
	}
	p, ok := blastLandPoint(st, blastRayIn{Origin: in.Origin, Dir: in.Dir, Max: in.Max})
	if !ok {
		return out
	}
	radius := in.Radius
	if radius <= 0 {
		radius = blastDefaultRadius
	}
	fuse := in.Fuse
	if fuse <= 0 {
		fuse = blastDefaultFuse
	}
	power := in.Power
	if power < 0 {
		power = blastDefaultPower
	}
	st.bombs = append(st.bombs, blastBomb{
		x: p.X, z: p.Z,
		radius: radius, fuse: fuse, total: fuse,
		power: power, fromX: st.throwerX, fromZ: st.throwerZ,
	})
	return blastThrowOut{
		Ok:     true,
		Index:  len(st.bombs) - 1,
		X:      p.X,
		Y:      p.Y,
		Z:      p.Z,
		Radius: radius,
		Fuse:   fuse,
	}
}

// blastCore 返回图元的"骨架点"：胶囊/线段取轴上最近点，球取球心，盒取体内最近点。
// 用来定"往哪边炸飞"：爆心正好压在目标上时表面点没有方向可言，
// 骨架点至少给出稳定的水平偏移（再退化就用投掷方向兜底）。
func blastCore(c collide.Vec3, s collide.Solid) collide.Vec3 {
	switch v := s.(type) {
	case collide.Capsule:
		return collide.ClosestPtPointSegment(c, v.A, v.B)
	case collide.Segment:
		return collide.ClosestPtPointSegment(c, v.A, v.B)
	case collide.Sphere:
		return v.C
	}
	return collide.ClosestSurfacePoint(c, s)
}

// blastSurfaceDist 爆心到图元表面的距离（爆心在目标内部时钳到 0）。
func blastSurfaceDist(c collide.Vec3, s collide.Solid) float64 {
	d := c.Distance(blastCore(c, s))
	switch v := s.(type) {
	case collide.Capsule:
		d -= v.R
	case collide.Sphere:
		d -= v.R
	}
	return math.Max(0, d)
}

// blastStep 推进一帧：目标漫游 + 击退速度 + 闪红衰减 → 同步进引擎 → 处理引信与爆炸。
// 先结算移动、再爆炸，所以爆炸用的是本帧目标的新位置。
func blastStep(in blastStepIn) blastStepOut {
	st := &blastWorld
	n := len(st.agents)
	out := blastStepOut{
		X:     make([]float64, n),
		Z:     make([]float64, n),
		Y:     make([]float64, n),
		Flash: make([]float64, n),
		Spin:  make([]float64, n),
		RollX: make([]float64, n),
		RollZ: make([]float64, n),
		Bombs: []blastBombOut{},
		Booms: []blastBoomOut{},
	}
	if st.engine == nil {
		return out
	}
	dt := in.Dt
	if dt <= 0 {
		dt = 1.0 / 60.0
	}
	if dt > 0.05 {
		dt = 0.05
	}
	speed := in.Speed
	if speed <= 0 {
		speed = 1.1
	}
	damping := in.Damping
	if damping <= 0 {
		damping = 3.0
	}
	st.tick++

	// 1) 目标：漫游（撞墙反弹）+ 击飞速度（水平 / 竖直 / 翻滚）+ 闪红衰减，
	//    然后把新图元（含离地高度）推给引擎。
	for i := range st.agents {
		a := &st.agents[i]
		airborne := a.y > 0 || a.vy > 0
		drag := damping // 地面摩擦
		if airborne {
			drag = blastAirDrag // 空中只有很小的空气阻力
		}
		decay := math.Exp(-drag * dt)
		a.x += (a.dx*speed*a.speed + a.vx) * dt
		a.z += (a.dz*speed*a.speed + a.vz) * dt
		a.vx *= decay
		a.vz *= decay
		if a.x > st.arena || a.x < -st.arena {
			a.dx, a.vx = -a.dx, -a.vx
			a.x = clampF(a.x, -st.arena, st.arena)
		}
		if a.z > st.arena || a.z < -st.arena {
			a.dz, a.vz = -a.dz, -a.vz
			a.z = clampF(a.z, -st.arena, st.arena)
		}
		// 竖直：被炸飞后走抛物线；落地要么小弹一下、要么趴下。
		if airborne {
			a.vy -= blastGravity * dt
			a.y += a.vy * dt
			if a.y <= 0 {
				a.y = 0
				if a.vy < -blastBounceMin {
					a.vy = -a.vy * blastRestitution
					a.vx *= blastBounceFric
					a.vz *= blastBounceFric
					a.spinV *= blastBounceFric
				} else {
					a.vy = 0
				}
			}
		}
		// 翻滚：空中按角速度转；落地后回正（引擎里的胶囊始终是直立的，视觉上也站回来）。
		if a.spinV != 0 || a.spin != 0 {
			a.spin += a.spinV * dt
			if a.y <= 0 {
				back := math.Exp(-blastSpinDecay * dt)
				a.spinV *= back
				a.spin *= back
				if math.Abs(a.spinV) < 1e-3 {
					a.spinV = 0
				}
				if math.Abs(a.spin) < 1e-3 {
					a.spin = 0
				}
			}
		}
		if a.flash > 0 {
			a.flash = math.Max(0, a.flash-blastFlashDecay*dt)
		}
		st.engine.Update(st.handles[i], blastCapsule(a.x, a.z, a.y, a.radius, a.height))
		out.X[i], out.Z[i], out.Y[i] = a.x, a.z, a.y
		out.Flash[i], out.Spin[i] = a.flash, a.spin
		out.RollX[i], out.RollZ[i] = a.rollX, a.rollZ
	}

	// 2) 引信：到点就爆；还没到点的按投掷顺序回给前端（顺序确定，便于对拍）。
	start := nowMs()
	live := st.bombs[:0]
	for _, b := range st.bombs {
		b.fuse -= dt
		if b.fuse > 0 {
			live = append(live, b)
			out.Bombs = append(out.Bombs, blastBombOut{
				X: b.x, Z: b.z, Radius: b.radius, Fuse: b.fuse, Total: b.total,
			})
			continue
		}
		out.Booms = append(out.Booms, blastExplode(st, b))
		st.explosions++
	}
	st.bombs = live
	out.Explosions = st.explosions
	out.HitTotal = st.hitTotal
	out.Ms = nowMs() - start
	return out
}

// blastExplode 结算一次爆炸：球体积查询 → 半径内的胶囊全部闪红 + 炸飞。
//
// 一个命中目标身上的三件事，全部来自这一次碰撞查询的返回值：
//   - 能量（闪红强度 + 击飞力度）= Result.Depth（穿透深度）÷ 半径；
//   - 方向 = 爆心 → 目标骨架点（爆心正好压在目标上时退化成投掷方向）；
//   - 抬升 = 接触点 Result.Point 的高度：接触点越靠脚，说明炸弹是在脚下炸的，飞得越高。
//
// 闪红强度取 max（不叠加）：连着两颗炸弹炸同一个人，闪红也不会超过 1。
func blastExplode(st *blastState, b blastBomb) blastBoomOut {
	out := blastBoomOut{X: b.x, Z: b.z, Radius: b.radius, Hits: []blastHitOut{}}
	q := blastQuery(collide.Vec3{X: b.x, Z: b.z}, b.radius)
	start := nowMs()
	st.engine.Query(q.Bounds(), func(collide.Handle) bool {
		out.Candidates++
		return true
	})
	st.engine.Overlap(q, nil, func(r collide.Result) bool {
		i, ok := st.index[r.Handle]
		if !ok {
			return true
		}
		a := &st.agents[i]
		// 强度 = 穿透深度 / 半径：擦边炸 → 0，爆心贴到目标身上 → 1（钳住）。
		// 球心到胶囊表面的距离 d 满足 Depth = 半径 - d，两种写法等价，但深度是查询直接给的。
		inten := 1.0
		if b.radius > 0 {
			inten = clampF(r.Depth/b.radius, 0, 1)
		}
		if inten > a.flash {
			a.flash = inten
		}
		// 击退方向 = 爆心 → 目标骨架点的水平分量。
		// 目标正好压在爆心上时这个方向没有意义（差一点点就翻转），退化成"顺着投掷方向推开"——
		// 确定性、也不会有"贴着爆心反而纹丝不动"的怪手感。
		core := blastCore(q.C, st.engine.Shape(r.Handle))
		push := collide.Vec3{X: core.X - q.C.X, Z: core.Z - q.C.Z}
		if push.Len() < blastPushDeadzone {
			push = collide.Vec3{X: q.C.X - b.fromX, Z: q.C.Z - b.fromZ}
		}
		var pushLen float64
		if l := push.Len(); l > 1e-6 {
			push = push.Scale(1 / l)
			pushLen = l
		}
		// 抬升：接触点在目标身上的位置决定"抬多高"——被炸到脚 = 从下往上掀，
		// 被炸到头 = 主要是平推。加一个基础抬升，保证任何命中都会离地。
		hRel := 1.0
		if a.height > 0 {
			hRel = clampF(1-(r.Point.Y-a.y)/a.height, 0, 1)
		}
		horiz := b.power * inten
		lift := blastLift * inten * (blastLiftBase + (1-blastLiftBase)*hRel)
		a.vx += push.X * horiz
		a.vz += push.Z * horiz
		a.vy += lift
		if pushLen > 1e-6 {
			a.rollX, a.rollZ = push.X, push.Z // 翻滚轴 = 竖直 × 击飞方向（前端按它把孩子翻过去）
		}
		a.spinV += blastSpin * inten * (blastSpinBase + (1-blastSpinBase)*hRel)
		st.hitTotal++
		out.Hits = append(out.Hits, blastHitOut{
			Index:     i,
			Depth:     r.Depth,
			Intensity: inten,
			Speed:     math.Hypot(horiz, lift),
			Point:     jv(r.Point),
			Normal:    jv(push),
			Lift:      lift,
		})
		return true
	})
	out.Ms = nowMs() - start
	return out
}
