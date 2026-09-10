// 本文件是「竖劈竞技场」场景的纯逻辑部分（不引用 syscall/js，宿主机可单测）。
//
// 与 swing 场景的区别：
//   - 所有角色都在移动（目标漫游、玩家追击），每帧都要把新图元同步进引擎；
//   - 挥砍是**竖直劈砍**：弧线在竖直平面里，绕「竖直 × 朝向」轴张开，
//     正角度朝上（举起）、负角度朝下（收刀）。
package main

import (
	"math"

	"starve/pkg/collide"
)

// arenaBody 是场景里一个角色的初始状态。
type arenaBody struct {
	X      float64 `json:"x"`
	Z      float64 `json:"z"`
	Radius float64 `json:"radius"`
	Height float64 `json:"height"`
	Dir    float64 `json:"dir"`   // 漫游方向（弧度）
	Speed  float64 `json:"speed"` // 漫游速度倍率
}

type arenaSetupIn struct {
	Player  arenaBody   `json:"player"`
	Targets []arenaBody `json:"targets"`
	Arena   float64     `json:"arena"` // 场地半边长
}

type arenaStepIn struct {
	Dt    float64 `json:"dt"`
	Chase bool    `json:"chase"` // 玩家是否追击最近目标
	Speed float64 `json:"speed"` // 目标漫游速度（米/秒）
	Reach float64 `json:"reach"` // 玩家追到多近停下
}

type arenaStepOut struct {
	PlayerX float64   `json:"playerX"`
	PlayerZ float64   `json:"playerZ"`
	Yaw     float64   `json:"yaw"` // 玩家朝向（弧度，+X 为 0、朝 +Z 增加）
	TargetX []float64 `json:"targetX"`
	TargetZ []float64 `json:"targetZ"`
	Bounced int       `json:"bounced"`
}

type arenaSwingIn struct {
	From      float64 `json:"from"`
	To        float64 `json:"to"`
	R0        float64 `json:"r0"`
	R1        float64 `json:"r1"`
	Thickness float64 `json:"thickness"`
	ChestY    float64 `json:"chestY"` // 挥砍绕的高度（胸口）
}

type arenaSwingOut struct {
	Candidates int         `json:"candidates"`
	Hits       []sectorHit `json:"hits"`
	Ms         float64     `json:"ms"`
}

type arenaAgent struct {
	x, z   float64
	dx, dz float64 // 单位方向
	speed  float64
}

type arenaState struct {
	engine  *collide.Engine
	player  collide.Handle
	targets []collide.Handle
	agents  []arenaAgent
	px, pz  float64 // 玩家当前坐标（引擎只存图元，渲染需要的坐标单独记一份）
	yaw     float64
	arena   float64
	tick    int
}

// 场景常量
const (
	arenaPlayerRadius = 0.4
	arenaPlayerHeight = 1.8
	arenaPlayerSpeed  = 2.6
	arenaTargetRadius = 0.35
	arenaTargetHeight = 1.7
	arenaChestY       = 1.15
)

var arenaWorld arenaState

// arenaSetup 建立场景：一个玩家 + 若干漫游目标，全部注册进引擎（只做一次）。
func arenaSetup(in arenaSetupIn) bool {
	eng := collide.NewEngine(collide.EngineOptions{Margin: 0.3})
	eng.Reserve(len(in.Targets) + 1)
	st := arenaState{engine: eng, arena: in.Arena, px: in.Player.X, pz: in.Player.Z, yaw: in.Player.Dir}
	if st.arena <= 0 {
		st.arena = 8
	}
	st.player = eng.Add(arenaCapsule(in.Player.X, in.Player.Z, in.Player.Radius, in.Player.Height))
	for _, t := range in.Targets {
		st.targets = append(st.targets, eng.Add(arenaCapsule(t.X, t.Z, t.Radius, t.Height)))
		sp := t.Speed
		if sp <= 0 {
			sp = 1
		}
		st.agents = append(st.agents, arenaAgent{
			x: t.X, z: t.Z,
			dx:    math.Cos(t.Dir),
			dz:    math.Sin(t.Dir),
			speed: sp,
		})
	}
	arenaWorld = st
	return true
}

// arenaCapsule 把一个角色变成胶囊图元（站在地面上）。
func arenaCapsule(x, z, radius, height float64) collide.Capsule {
	if height <= 0 {
		height = arenaTargetHeight
	}
	return collide.Capsule{
		A: collide.Vec3{X: x, Z: z},
		B: collide.Vec3{X: x, Y: height, Z: z},
		R: radius,
	}
}

// arenaStep 推进一帧：目标漫游（撞墙反弹）+ 玩家转身面向最近目标并追击，
// 然后把所有人的新图元同步进引擎——这就是 Update 的日常用法。
func arenaStep(in arenaStepIn) arenaStepOut {
	st := &arenaWorld
	n := len(st.agents)
	out := arenaStepOut{TargetX: make([]float64, n), TargetZ: make([]float64, n)}
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
	reach := in.Reach
	if reach <= 0 {
		reach = 1.5
	}
	st.tick++

	for i := range st.agents {
		a := &st.agents[i]
		a.x += a.dx * speed * a.speed * dt
		a.z += a.dz * speed * a.speed * dt
		if a.x > st.arena || a.x < -st.arena {
			a.dx = -a.dx
			a.x = clampF(a.x, -st.arena, st.arena)
			out.Bounced++
		}
		if a.z > st.arena || a.z < -st.arena {
			a.dz = -a.dz
			a.z = clampF(a.z, -st.arena, st.arena)
			out.Bounced++
		}
		st.engine.Update(st.targets[i], arenaCapsule(a.x, a.z, arenaTargetRadius, arenaTargetHeight))
		out.TargetX[i], out.TargetZ[i] = a.x, a.z
	}

	// 玩家：面向最近目标；追到 reach 之内就停下
	if near := arenaNearest(st, st.px, st.pz); near >= 0 {
		a := st.agents[near]
		dx, dz := a.x-st.px, a.z-st.pz
		st.yaw = math.Atan2(dz, dx)
		if in.Chase {
			dist := math.Hypot(dx, dz)
			if dist > reach {
				step := math.Min(arenaPlayerSpeed*dt, dist-reach)
				st.px += dx / dist * step
				st.pz += dz / dist * step
			}
		}
	}
	st.engine.Update(st.player, arenaCapsule(st.px, st.pz, arenaPlayerRadius, arenaPlayerHeight))

	out.PlayerX, out.PlayerZ, out.Yaw = st.px, st.pz, st.yaw
	return out
}

// arenaNearest 返回最近目标的索引（没有则 -1）。
func arenaNearest(st *arenaState, x, z float64) int {
	best, bestD := -1, math.Inf(1)
	for i := range st.agents {
		d := math.Hypot(st.agents[i].x-x, st.agents[i].z-z)
		if d < bestD {
			best, bestD = i, d
		}
	}
	return best
}

// arenaSwing 做一次竖直劈砍的扇形查询。
//
// 调用方只给角度区间、射程、厚度这些**数字**；扇形体积（旋转轴、参考方向、
// 所在平面）全部由引擎按玩家当前朝向算出来——竖直劈砍的轴 = 竖直 × 朝向。
func arenaSwing(in arenaSwingIn) arenaSwingOut {
	st := &arenaWorld
	out := arenaSwingOut{Hits: []sectorHit{}}
	if st.engine == nil {
		return out
	}
	r0, r1 := in.R0, in.R1
	if r1 <= r0 {
		r1 = r0 + 0.1
	}
	chest := in.ChestY
	if chest <= 0 {
		chest = arenaChestY
	}
	face := collide.Vec3{X: math.Cos(st.yaw), Z: math.Sin(st.yaw)}
	sector := collide.Sector{
		Center:    collide.Vec3{X: st.px, Y: chest, Z: st.pz},
		Axis:      collide.YAxis.Cross(face),
		Ref:       face,
		From:      in.From,
		To:        in.To,
		R0:        r0,
		R1:        r1,
		Thickness: in.Thickness,
	}

	start := nowMs()
	st.engine.Query(sector.Bounds(), func(collide.Handle) bool {
		out.Candidates++
		return true
	})
	idx := make(map[collide.Handle]int, len(st.targets))
	for i, h := range st.targets {
		idx[h] = i
	}
	st.engine.OverlapSector(sector, func(h collide.Handle) bool { return h != st.player },
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
