package world

import (
	"fmt"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
)

// benchAOIWorld 构造 w×h 世界 + 指定数量的感知者/玩家（确定性散布）。
func benchAOIWorld(b *testing.B, w, h, creatures, radius, players int) *WorldActor {
	b.Helper()
	wa := NewWorldActor(WorldConfig{})
	wa.sim.AddResource(&MapData{Width: w, Height: h, CornerTypes: make([]byte, (w+1)*(h+1))})
	for i := 0; i < creatures; i++ {
		e := wa.sim.CreateEntity()
		ecs.Add(wa.sim, e, components.Position{X: (i * 541) % w, Y: (i * 1049) % h})
		ecs.Add(wa.sim, e, components.Health{Cur: 10, Max: 10})
		ecs.Add(wa.sim, e, components.AOI{Radius: radius})
	}
	for i := 0; i < players; i++ {
		p := wa.createPlayer(fmt.Sprintf("u%d", i))
		ecs.Set(wa.sim, p, components.Position{X: (i * 647) % w, Y: (i * 1291) % h})
	}
	return wa
}

// BenchmarkAOISystem：稀疏/密集两档，度量单次感知结算的耗时与分配。
func BenchmarkAOISystem(b *testing.B) {
	cases := []struct {
		name      string
		w, h      int
		creatures int
		radius    int
		players   int
	}{
		{"sparse_30x6", 80, 80, 30, 6, 10},
		{"dense_100x8", 80, 80, 100, 8, 20},
		{"worst_200x12", 80, 80, 200, 12, 50},
		{"huge_5000x512", 512, 512, 5000, 6, 100},
		{"huge_5000x1024", 1024, 1024, 5000, 6, 100},
		{"huge_10000x1024", 1024, 1024, 10000, 6, 100},
		{"huge_10000x2048", 2048, 2048, 10000, 6, 100},
		{"reset_1024_empty", 1024, 1024, 0, 6, 0}, // 隔离网格清零固定成本
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			wa := benchAOIWorld(b, tc.w, tc.h, tc.creatures, tc.radius, tc.players)
			aoi := &systems.AOISystem{}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				aoi.Update(wa.sim, time.Millisecond)
			}
		})
	}
}
