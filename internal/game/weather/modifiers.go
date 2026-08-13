package weather

import (
	"math"
	"sort"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// applyModifiers 局部天气修正：风扇（风力叠加 + 下风向降雾）、热源（升温 + 降雾）。
// 遍历实体 ID 升序（确定性）；曼哈顿距离，falloff 线性衰减。
func applyModifiers(w *ecs.World, q WeatherQuery, out *WeatherSample) {
	type fan struct {
		e ecs.Entity
		c *components.Fan
		p *components.Position
	}
	var fans []fan
	ecs.Query2[components.Fan, components.Position](w, func(e ecs.Entity, f *components.Fan, p *components.Position) {
		fans = append(fans, fan{e, f, p})
	})
	sort.Slice(fans, func(i, j int) bool { return fans[i].e < fans[j].e })
	for _, f := range fans {
		dist := abs(q.X-f.p.X) + abs(q.Y-f.p.Y)
		if f.c.Radius <= 0 || dist > f.c.Radius {
			continue
		}
		wgt := 1 - float32(dist)/float32(f.c.Radius+1)
		fx, fy := normDir(f.c.DirX, f.c.DirY)
		if fx == 0 && fy == 0 {
			continue
		}
		out.WindDirX += fx * float32(f.c.Strength) * wgt
		out.WindDirY += fy * float32(f.c.Strength) * wgt
		if n := float32(math.Hypot(float64(out.WindDirX), float64(out.WindDirY))); n > 0 {
			out.WindDirX /= n
			out.WindDirY /= n
		}
		out.WindSpeed += float32(f.c.Strength) * wgt
		// 下风向（查询点相对风扇的投影沿风扇方向为正）雾减
		if float32(q.X-f.p.X)*fx+float32(q.Y-f.p.Y)*fy > 0 {
			out.Fog -= 0.2 * wgt
		}
	}

	type heat struct {
		e ecs.Entity
		c *components.HeatSource
		p *components.Position
	}
	var heats []heat
	ecs.Query2[components.HeatSource, components.Position](w, func(e ecs.Entity, h *components.HeatSource, p *components.Position) {
		heats = append(heats, heat{e, h, p})
	})
	sort.Slice(heats, func(i, j int) bool { return heats[i].e < heats[j].e })
	for _, h := range heats {
		dist := abs(q.X-h.p.X) + abs(q.Y-h.p.Y)
		if h.c.Radius <= 0 || dist > h.c.Radius {
			continue
		}
		wgt := 1 - float32(dist)/float32(h.c.Radius+1)
		out.Temperature += float32(h.c.Strength) * wgt
		out.Fog -= 0.15 * wgt
	}
	out.Fog = clamp01(out.Fog)
	out.Temperature = clamp(out.Temperature, -30, 45)
	out.WindSpeed = clamp(out.WindSpeed, 0, 15)
}

// normDir 把 (-1/0/1) 方向归一化为单位向量（零向量返回 0,0）。
func normDir(dx, dy int) (float32, float32) {
	fx, fy := float32(dx), float32(dy)
	n := float32(math.Hypot(float64(fx), float64(fy)))
	if n == 0 {
		return 0, 0
	}
	return fx / n, fy / n
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
