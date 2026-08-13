package weather

import (
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/effect"
	"starve/internal/game/worldmap"
)

// init 注册天气派生效果提供者：温度 → 寒冷/炎热效果。
// 与覆盖源（地块/发射器）同语义：EffectSystem 每 tick 一并计算 Enter/Tick/Exit，
// 离开阈值自动解除（进快照，客户端可显示 buff）。
func init() {
	effect.RegisterDerivedEffectsProvider(climateEffects)
}

// climateEffects 按玩家位置采样天气，把冷/热转成效果（param = 每 tick 伤害）。
func climateEffects(w *ecs.World, e ecs.Entity) map[components.EffectOrder]int32 {
	wr, ok := ecs.TryResource[components.Weather](w)
	if !ok {
		return nil
	}
	if !ecs.Has[components.Position](w, e) {
		return nil
	}
	p := ecs.Get[components.Position](w, e)
	q := WeatherQuery{
		X:      p.X,
		Y:      p.Y,
		Tick:   wr.Phase,
		Season: components.SeasonOf(wr.Phase, wr.YearTicks),
	}
	if md, ok := ecs.TryResource[worldmap.MapData](w); ok {
		q.Height, q.TileType = md.TileAt(p.X, p.Y)
	}
	s := SampleAt(w, q)
	out := map[components.EffectOrder]int32{}
	if wr.ColdDamage > 0 && s.Temperature <= wr.ColdAt {
		out[components.EffectCold] = int32(wr.ColdDamage)
	}
	if wr.HeatDamage > 0 && s.Temperature >= wr.HeatAt {
		out[components.EffectHeat] = int32(wr.HeatDamage)
	}
	return out
}
