package world

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

func TestMoveSlopeSlowsSouthDrop(t *testing.T) {
	run := func(t *testing.T, wa *WorldActor) float64 {
		t.Helper()
		p := wa.createPlayer("u1")
		ecs.Set(wa.sim, p, components.Position{X: 0, Y: 0})
		ecs.Set(wa.sim, p, components.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5, DirY: 1})
		tickWorld(wa)
		pos := ecs.Get[components.Position](wa.sim, p)
		mv := ecs.Get[components.Moveable](wa.sim, p)
		if mv.EffectiveSpeed != 10 {
			t.Fatalf("EffectiveSpeed=%v want 10（效果速度，不含坡度）", mv.EffectiveSpeed)
		}
		return float64(pos.Y) + mv.SubY
	}
	flatY := run(t, moveTestWorld())
	slopeY := run(t, southDropWorld())
	if math.Abs(flatY-1) > 1e-6 {
		t.Fatalf("平地 +Y 1 tick 应到 y=1，实际 %v", flatY)
	}
	if slopeY >= flatY-1e-6 {
		t.Fatalf("下坡应变慢: slope=%v flat=%v", slopeY, flatY)
	}
}

func southDropWorld() *WorldActor {
	wa := NewWorldActor(WorldConfig{})
	md := &MapData{
		Width:         8,
		Height:        8,
		CornerHeights: make([]byte, 9*9),
		CornerTypes:   make([]byte, 9*9),
	}
	for x := 0; x <= 8; x++ {
		md.CornerHeights[x] = 1
	}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = 3
	}
	wa.sim.AddResource(md)
	return wa
}
