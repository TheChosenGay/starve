// 本文件是 wasm 桥接的“纯逻辑”部分：不引用 syscall/js，因此可以在宿主机上编译和单测。
// 与浏览器交互的胶水（main / eval / collideSim）在 main_wasm.go。
package main

import (
	"encoding/json"
	"math"

	"starve/pkg/collide"
)

// ---- JSON 传输类型 ----

type vec3j struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type aabbj struct {
	Min vec3j `json:"min"`
	Max vec3j `json:"max"`
}

type obbj struct {
	C vec3j      `json:"c"`
	U [3]vec3j   `json:"u"`
	E [3]float64 `json:"e"`
}

type capsulej struct {
	A vec3j   `json:"a"`
	B vec3j   `json:"b"`
	R float64 `json:"r"`
}

type spherej struct {
	C vec3j   `json:"c"`
	R float64 `json:"r"`
}

type scene struct {
	Mode     string    `json:"mode"`
	Point    *vec3j    `json:"point"`
	Segment  []vec3j   `json:"segment"`
	Triangle []vec3j   `json:"triangle"`
	AABB     *aabbj    `json:"aabb"`
	OBB      *obbj     `json:"obb"`
	CapsuleA *capsulej `json:"capsuleA"`
	CapsuleB *capsulej `json:"capsuleB"`
	Sphere   *spherej  `json:"sphere"`
}

type result struct {
	Mode     string    `json:"mode"`
	Overlap  bool      `json:"overlap"`
	Closest  *vec3j    `json:"closest,omitempty"`
	Closest2 *vec3j    `json:"closest2,omitempty"`
	Contact  *contactj `json:"contact,omitempty"`
	DistSq   float64   `json:"distSq"`
	Dist     float64   `json:"dist"`
	S        *float64  `json:"s,omitempty"`
	T        *float64  `json:"t,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type contactj struct {
	Normal vec3j   `json:"normal"`
	Depth  float64 `json:"depth"`
	Point  vec3j   `json:"point"`
}

// ---- 复杂场景：一堆球在盒子里下落、互相碰撞 ----

type simBody struct {
	P vec3j   `json:"p"`
	V vec3j   `json:"v"`
	R float64 `json:"r"`
}

type simIn struct {
	Bodies   []simBody `json:"bodies"`
	Dt       float64   `json:"dt"`
	Gravity  float64   `json:"gravity"`
	FloorY   float64   `json:"floorY"`
	Bound    float64   `json:"bound"` // x/z 方向半边长（正方形围栏）
	Rest     float64   `json:"rest"`
	Friction float64   `json:"friction"`
}

// simContact 是接触点的传输形式。注意：每个字段必须各有自己的 json tag，
// 否则 X/Y/Z 会共用同一个名字、在序列化时被整体丢弃（这个坑踩过一次）。
type simContact struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
	R float64 `json:"r"`
}

type simOut struct {
	Bodies   []simBody    `json:"bodies"`
	Contacts []simContact `json:"contacts"`
}

// ---- 投射场景（子弹 / 劈砍）：一个球沿 motion 扫掠，对一组混合形状求首次命中 ----

type castTarget struct {
	Kind string  `json:"kind"` // "aabb" | "capsule"
	Min  *vec3j  `json:"min"`
	Max  *vec3j  `json:"max"`
	A    *vec3j  `json:"a"`
	B    *vec3j  `json:"b"`
	R    float64 `json:"r"`
}

type castIn struct {
	Sphere  spherej      `json:"sphere"`
	Motion  vec3j        `json:"motion"`
	Targets []castTarget `json:"targets"`
}

type castHit struct {
	Index  int     `json:"index"`
	Kind   string  `json:"kind"`
	T      float64 `json:"t"`
	Dist   float64 `json:"dist"`
	Point  vec3j   `json:"point"`
	Normal vec3j   `json:"normal"`
}

type castOut struct {
	Hit  bool      `json:"hit"`
	Best castHit   `json:"best"`
	All  []castHit `json:"all"`
}

// castScene 把一个移动的球对一组目标做扫掠，返回最早命中的那个（以及全部命中）。
// 这三个演示场景（子弹 / 劈砍 / 被墙挡住）都走这里。
func castScene(in castIn) castOut {
	s := collide.Sphere{C: v3(in.Sphere.C), R: in.Sphere.R}
	motion := v3(in.Motion)
	out := castOut{All: []castHit{}}
	bestT := math.Inf(1)
	for i, tgt := range in.Targets {
		var h collide.Hit
		switch tgt.Kind {
		case "aabb":
			if tgt.Min == nil || tgt.Max == nil {
				continue
			}
			h = collide.SweepSphereAABB(s, motion, collide.AABB{Min: v3(*tgt.Min), Max: v3(*tgt.Max)})
		case "capsule":
			if tgt.A == nil || tgt.B == nil {
				continue
			}
			h = collide.SweepSphereCapsule(s, motion, collide.Capsule{A: v3(*tgt.A), B: v3(*tgt.B), R: tgt.R})
		default:
			continue
		}
		if !h.Hit {
			continue
		}
		ch := castHit{Index: i, Kind: tgt.Kind, T: h.T, Dist: h.Dist, Point: jv(h.Point), Normal: jv(h.Normal)}
		out.All = append(out.All, ch)
		if h.T < bestT {
			bestT, out.Best, out.Hit = h.T, ch, true
		}
	}
	return out
}

// ---- 劈砍：刀是 OBB，按角度细分扫掠（旋转运动的保守前进）----

type swingBlade struct {
	Origin     vec3j   `json:"origin"`
	Length     float64 `json:"length"`
	HalfHeight float64 `json:"halfHeight"` // 刀身厚度方向（竖直）
	HalfWidth  float64 `json:"halfWidth"`  // 刀身宽度方向（挥砍平面内）
}

type swingIn struct {
	Blade     swingBlade   `json:"blade"`
	AngleFrom float64      `json:"angleFrom"`
	AngleTo   float64      `json:"angleTo"`
	SubSteps  int          `json:"subSteps"`
	Targets   []castTarget `json:"targets"`
}

type swingHit struct {
	Index  int     `json:"index"`
	Kind   string  `json:"kind"`
	Angle  float64 `json:"angle"`
	Depth  float64 `json:"depth"`
	Point  vec3j   `json:"point"`
	Normal vec3j   `json:"normal"`
}

type swingOut struct {
	Hit  bool       `json:"hit"`
	Best swingHit   `json:"best"`
	All  []swingHit `json:"all"`
}

// bladeOBB 按挥砍角度构造刀的 OBB：刀从 origin 沿 (cosθ, 0, sinθ) 伸出 length。
// 局部轴：U0 = 刀身方向，U1 = 竖直向上，U2 = 挥砍平面内的侧向。
func bladeOBB(b swingBlade, angle float64) collide.OBB {
	dir := collide.Vec3{X: math.Cos(angle), Z: math.Sin(angle)}
	up := collide.Vec3{Y: 1}
	side := dir.Cross(up)
	return collide.OBB{
		C: v3(b.Origin).Add(dir.Scale(b.Length * 0.5)),
		U: [3]collide.Vec3{dir, up, side},
		E: [3]float64{b.Length * 0.5, b.HalfHeight, b.HalfWidth},
	}
}

// contactBlade 用刀的 OBB 去撞一个目标（胶囊 / AABB）。
func contactBlade(blade collide.OBB, tgt castTarget) (collide.Contact, bool) {
	switch tgt.Kind {
	case "capsule":
		if tgt.A == nil || tgt.B == nil {
			return collide.Contact{}, false
		}
		c := collide.Capsule{A: v3(*tgt.A), B: v3(*tgt.B), R: tgt.R}
		return collide.ContactCapsuleOBB(c, blade)
	case "aabb":
		if tgt.Min == nil || tgt.Max == nil {
			return collide.Contact{}, false
		}
		b := collide.AABBToOBB(collide.AABB{Min: v3(*tgt.Min), Max: v3(*tgt.Max)})
		return collide.ContactOBBOBB(blade, b)
	default:
		return collide.Contact{}, false
	}
}

// swingScene 把挥砍的角区间细分成 subSteps 步，逐步用刀的 OBB 做接触测试，
// 返回最早命中（对应最小的角度步）。“细分”就是旋转运动的保守前进，防快速挥砍穿透。
func swingScene(in swingIn) swingOut {
	sub := in.SubSteps
	if sub < 1 {
		sub = 8
	}
	out := swingOut{All: []swingHit{}}
	for k := 0; k < sub; k++ {
		f := float64(k+1) / float64(sub)
		angle := in.AngleFrom + (in.AngleTo-in.AngleFrom)*f
		blade := bladeOBB(in.Blade, angle)
		for i, tgt := range in.Targets {
			ct, ok := contactBlade(blade, tgt)
			if !ok {
				continue
			}
			h := swingHit{
				Index: i, Kind: tgt.Kind, Angle: angle, Depth: ct.Depth,
				Point: jv(ct.Point), Normal: jv(ct.Normal),
			}
			out.All = append(out.All, h)
			out.Best, out.Hit = h, true
			return out // 最早命中的子步即为结果
		}
	}
	return out
}

// ---- 辅助 ----

func v3(v vec3j) collide.Vec3    { return collide.Vec3{X: v.X, Y: v.Y, Z: v.Z} }
func jv(v collide.Vec3) vec3j    { return vec3j{X: v.X, Y: v.Y, Z: v.Z} }
func fptr(f float64) *float64    { return &f }
func jptr(v collide.Vec3) *vec3j { p := jv(v); return &p }

func jc(c collide.Contact) *contactj {
	return &contactj{Normal: jv(c.Normal), Depth: c.Depth, Point: jv(c.Point)}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// compute 把单个场景查询交给 pkg/collide 计算，并打包成结果 JSON。
func compute(sc scene) result {
	res := result{Mode: sc.Mode}
	switch sc.Mode {
	case "pointSegment":
		if sc.Point == nil || len(sc.Segment) < 2 {
			res.Error = "pointSegment needs point + segment[2]"
			return res
		}
		p := v3(*sc.Point)
		c := collide.ClosestPtPointSegment(p, v3(sc.Segment[0]), v3(sc.Segment[1]))
		res.Closest = jptr(c)
		res.DistSq = c.DistanceSq(p)
	case "pointTriangle":
		if sc.Point == nil || len(sc.Triangle) < 3 {
			res.Error = "pointTriangle needs point + triangle[3]"
			return res
		}
		p := v3(*sc.Point)
		c := collide.ClosestPtPointTriangle(p, v3(sc.Triangle[0]), v3(sc.Triangle[1]), v3(sc.Triangle[2]))
		res.Closest = jptr(c)
		res.DistSq = c.DistanceSq(p)
	case "pointAABB":
		if sc.Point == nil || sc.AABB == nil {
			res.Error = "pointAABB needs point + aabb"
			return res
		}
		p := v3(*sc.Point)
		b := collide.AABB{Min: v3(sc.AABB.Min), Max: v3(sc.AABB.Max)}
		res.Closest = jptr(collide.ClosestPtPointAABB(p, b))
		res.DistSq = collide.SqDistPointAABB(p, b)
	case "pointOBB":
		if sc.Point == nil || sc.OBB == nil {
			res.Error = "pointOBB needs point + obb"
			return res
		}
		p := v3(*sc.Point)
		o := collide.OBB{
			C: v3(sc.OBB.C),
			U: [3]collide.Vec3{v3(sc.OBB.U[0]), v3(sc.OBB.U[1]), v3(sc.OBB.U[2])},
			E: sc.OBB.E,
		}
		res.Closest = jptr(collide.ClosestPtPointOBB(p, o))
		res.DistSq = collide.SqDistPointOBB(p, o)
	case "sphereAABB":
		if sc.Sphere == nil || sc.AABB == nil {
			res.Error = "sphereAABB needs sphere + aabb"
			return res
		}
		s := collide.Sphere{C: v3(sc.Sphere.C), R: sc.Sphere.R}
		b := collide.AABB{Min: v3(sc.AABB.Min), Max: v3(sc.AABB.Max)}
		res.Overlap = collide.TestSphereAABB(s, b)
		res.Closest = jptr(collide.ClosestPtPointAABB(s.C, b))
		res.DistSq = collide.SqDistPointAABB(s.C, b)
		if ct, ok := collide.ContactSphereAABB(s, b); ok {
			res.Contact = jc(ct)
		}
	case "sphereOBB":
		if sc.Sphere == nil || sc.OBB == nil {
			res.Error = "sphereOBB needs sphere + obb"
			return res
		}
		s := collide.Sphere{C: v3(sc.Sphere.C), R: sc.Sphere.R}
		o := collide.OBB{
			C: v3(sc.OBB.C),
			U: [3]collide.Vec3{v3(sc.OBB.U[0]), v3(sc.OBB.U[1]), v3(sc.OBB.U[2])},
			E: sc.OBB.E,
		}
		res.Overlap = collide.TestSphereOBB(s, o)
		res.Closest = jptr(collide.ClosestPtPointOBB(s.C, o))
		res.DistSq = collide.SqDistPointOBB(s.C, o)
		if ct, ok := collide.ContactSphereOBB(s, o); ok {
			res.Contact = jc(ct)
		}
	case "sphereTriangle":
		if sc.Sphere == nil || len(sc.Triangle) < 3 {
			res.Error = "sphereTriangle needs sphere + triangle[3]"
			return res
		}
		s := collide.Sphere{C: v3(sc.Sphere.C), R: sc.Sphere.R}
		a, b, c := v3(sc.Triangle[0]), v3(sc.Triangle[1]), v3(sc.Triangle[2])
		res.Overlap = collide.TestSphereTriangle(s, a, b, c)
		res.Closest = jptr(collide.ClosestPtPointTriangle(s.C, a, b, c))
		res.DistSq = collide.SqDistPointTriangle(s.C, a, b, c)
	case "sphereCapsule":
		if sc.Sphere == nil || sc.CapsuleA == nil {
			res.Error = "sphereCapsule needs sphere + capsuleA"
			return res
		}
		s := collide.Sphere{C: v3(sc.Sphere.C), R: sc.Sphere.R}
		cap := collide.Capsule{A: v3(sc.CapsuleA.A), B: v3(sc.CapsuleA.B), R: sc.CapsuleA.R}
		res.Overlap = collide.TestSphereCapsule(s, cap)
		res.Closest = jptr(collide.ClosestPtPointSegment(s.C, cap.A, cap.B))
		res.DistSq = collide.SqDistPointSegment(cap.A, cap.B, s.C)
		if ct, ok := collide.ContactSphereCapsule(s, cap); ok {
			res.Contact = jc(ct)
		}
	case "capsuleCapsule":
		if sc.CapsuleA == nil || sc.CapsuleB == nil {
			res.Error = "capsuleCapsule needs capsuleA + capsuleB"
			return res
		}
		a := collide.Capsule{A: v3(sc.CapsuleA.A), B: v3(sc.CapsuleA.B), R: sc.CapsuleA.R}
		b := collide.Capsule{A: v3(sc.CapsuleB.A), B: v3(sc.CapsuleB.B), R: sc.CapsuleB.R}
		res.Overlap = collide.TestCapsuleCapsule(a, b)
		s, t, c1, c2, d2 := collide.ClosestPtSegmentSegment(a.A, a.B, b.A, b.B)
		res.S, res.T = fptr(s), fptr(t)
		res.Closest, res.Closest2 = jptr(c1), jptr(c2)
		res.DistSq = d2
		if ct, ok := collide.ContactCapsuleCapsule(a, b); ok {
			res.Contact = jc(ct)
		}
	case "aabbAABB":
		if sc.AABB == nil || sc.OBB == nil {
			res.Error = "aabbAABB needs aabb + (second box in obb field)"
			return res
		}
		a := collide.AABB{Min: v3(sc.AABB.Min), Max: v3(sc.AABB.Max)}
		o := sc.OBB
		b := collide.OBB{
			C: v3(o.C),
			U: [3]collide.Vec3{v3(o.U[0]), v3(o.U[1]), v3(o.U[2])},
			E: o.E,
		}
		half := collide.Vec3{X: b.E[0], Y: b.E[1], Z: b.E[2]}
		ab := collide.AABB{Min: b.C.Sub(half), Max: b.C.Add(half)}
		res.Overlap = collide.TestAABBAABB(a, ab)
	default:
		res.Error = "unknown mode: " + sc.Mode
	}
	res.Dist = math.Sqrt(res.DistSq)
	return res
}

// simStep 用 pkg/collide 的接触查询驱动一个极简刚体步进：
// 积分 → 平面约束 → 两两碰撞 → 返回接触点。只为压力测试碰撞检测。
func simStep(in simIn) simOut {
	type body struct {
		p, v collide.Vec3
		r    float64
	}
	bs := make([]body, len(in.Bodies))
	for i, b := range in.Bodies {
		bs[i] = body{p: v3(b.P), v: v3(b.V), r: b.R}
	}

	dt, rest, fric := in.Dt, in.Rest, in.Friction
	if dt <= 0 {
		dt = 1.0 / 60.0
	}
	const vmax = 60.0
	out := simOut{Contacts: []simContact{}}

	// 1) 积分
	for i := range bs {
		bs[i].v.Y -= in.Gravity * dt
		damp := math.Pow(0.999, dt*60)
		bs[i].v = bs[i].v.Scale(damp)
		bs[i].p = bs[i].p.Add(bs[i].v.Scale(dt))
		if s := bs[i].v.Len(); s > vmax {
			bs[i].v = bs[i].v.Scale(vmax / s)
		}
	}

	// 2) 地板与四壁（点到平面距离直接复用 pkg/collide）
	planes := []collide.Plane{
		{N: collide.Vec3{Y: 1}, D: in.FloorY},  // 地板
		{N: collide.Vec3{X: 1}, D: -in.Bound},  // x = -bound
		{N: collide.Vec3{X: -1}, D: -in.Bound}, // x = +bound
		{N: collide.Vec3{Z: 1}, D: -in.Bound},  // z = -bound
		{N: collide.Vec3{Z: -1}, D: -in.Bound}, // z = +bound
	}
	for i := range bs {
		for _, pl := range planes {
			d := collide.SignedDistancePointPlane(bs[i].p, pl)
			if d >= bs[i].r {
				continue
			}
			bs[i].p = bs[i].p.Add(pl.N.Scale(bs[i].r - d))
			if vn := bs[i].v.Dot(pl.N); vn < 0 {
				bs[i].v = bs[i].v.Sub(pl.N.Scale(vn * (1 + rest)))
				tang := bs[i].v.Sub(pl.N.Scale(bs[i].v.Dot(pl.N)))
				bs[i].v = bs[i].v.Sub(tang.Scale(fric))
			}
			cp := bs[i].p.Sub(pl.N.Scale(bs[i].r))
			out.Contacts = append(out.Contacts, simContact{X: cp.X, Y: cp.Y, Z: cp.Z, R: bs[i].r})
		}
	}

	// 3) 两两碰撞（球-球）：接触信息走 collide.ContactSphereSphere
	for i := 0; i < len(bs); i++ {
		for j := i + 1; j < len(bs); j++ {
			ct, ok := collide.ContactSphereSphere(
				collide.Sphere{C: bs[i].p, R: bs[i].r},
				collide.Sphere{C: bs[j].p, R: bs[j].r},
			)
			if !ok {
				continue
			}
			n := ct.Normal // 从 j 指向 i；沿 +n 推开 i
			bs[i].p = bs[i].p.Add(n.Scale(ct.Depth * 0.5))
			bs[j].p = bs[j].p.Sub(n.Scale(ct.Depth * 0.5))
			if vn := bs[i].v.Sub(bs[j].v).Dot(n); vn < 0 {
				imp := -(1 + rest) * vn * 0.5
				bs[i].v = bs[i].v.Add(n.Scale(imp))
				bs[j].v = bs[j].v.Sub(n.Scale(imp))
			}
			out.Contacts = append(out.Contacts, simContact{
				X: ct.Point.X, Y: ct.Point.Y, Z: ct.Point.Z,
				R: 0.5 * (bs[i].r + bs[j].r),
			})
		}
	}

	// 4) 输出
	out.Bodies = make([]simBody, len(bs))
	for i, b := range bs {
		out.Bodies[i] = simBody{P: jv(b.p), V: jv(b.v), R: b.r}
	}
	return out
}
