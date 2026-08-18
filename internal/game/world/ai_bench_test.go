package world

import (
	"fmt"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/systems"
)

// spawnAICreature 造一只生物（wolf/rabbit），wolf 猎兔 + 敌视玩家，rabbit 低血逃跑。
func spawnAICreature(b testing.TB, wa *WorldActor, x, y int, wolf bool) ecs.Entity {
	b.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	hp, iv, rad := 10, 3, 0
	if wolf {
		hp, iv, rad = 30, 2, 6
	}
	ecs.Add(wa.sim, e, components.Health{Cur: hp, Max: hp})
	ecs.Add(wa.sim, e, components.Attackable{})
	ecs.Add(wa.sim, e, components.Moveable{Speed: intervalToSpeed(iv, 0.05)})
	ecs.Add(wa.sim, e, components.AOI{Radius: rad})

	kind := components.CreatureRabbit
	ai := components.AI{State: components.CreatureIdle, HitMemoryTicks: 5}
	wp := interactive.Attacker{AttackRange: 1}
	if wolf {
		kind = components.CreatureWolf
		ai.HostilePlayers = true
		ai.HostileKinds = []components.CreatureKind{components.CreatureRabbit}
		wp = interactive.Attacker{AttackRange: 1, AttackDamage: 8, AttackCooldown: 20}
	} else {
		ai.FleeHP = 5
	}
	ecs.Add(wa.sim, e, components.Creature{Kind: kind, Threats: map[ecs.Entity]int32{}, HomeX: x, HomeY: y, RoamRadius: 0})
	ecs.Add(wa.sim, e, ai)
	ecs.Add(wa.sim, e, wp)
	return e
}

// buildAIWorld 规模世界：w×h 地图 + 狼/兔/玩家（确定性散布）。
func buildAIWorld(b testing.TB, w, h, wolves, rabbits, players int) *WorldActor {
	b.Helper()
	wa := NewWorldActor(WorldConfig{})
	wa.sim.AddResource(&MapData{Width: w, Height: h, CornerTypes: make([]byte, (w+1)*(h+1))})
	for i := 0; i < wolves; i++ {
		spawnAICreature(b, wa, (i*541)%w, (i*1049)%h, true)
	}
	for i := 0; i < rabbits; i++ {
		spawnAICreature(b, wa, (i*811)%w, (i*1237)%h, false)
	}
	for i := 0; i < players; i++ {
		p := wa.createPlayer(fmt.Sprintf("p%d", i))
		ecs.Set(wa.sim, p, components.Position{X: (i * 647) % w, Y: (i * 1291) % h})
	}
	return wa
}

// BenchmarkAI5000：5000 只生物（2500 狼 + 2500 兔）在四档地图下的"感知+AI+移动"耗时，
// 并拆开每个系统单独测（看分配/耗时分布）。
func BenchmarkAI5000(b *testing.B) {
	cases := []struct {
		name            string
		w, h            int
		wolves, rabbits int
		players         int
	}{
		{"mixed_256", 256, 256, 2500, 2500, 20},
		{"mixed_512", 512, 512, 2500, 2500, 20},
		{"mixed_1024", 1024, 1024, 2500, 2500, 20},
		{"mixed_2048", 2048, 2048, 2500, 2500, 20},
	}
	for _, tc := range cases {
		wa := buildAIWorld(b, tc.w, tc.h, tc.wolves, tc.rabbits, tc.players)
		b.Run(tc.name, func(b *testing.B) {
			aoi, ai, mv := &systems.AOISystem{}, &systems.AISystem{}, &systems.MoveSystem{}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				aoi.Update(wa.sim, time.Millisecond)
				ai.Update(wa.sim, time.Millisecond)
				mv.Update(wa.sim, time.Millisecond)
			}
		})
		b.Run(tc.name+"/aoi", func(b *testing.B) {
			aoi := &systems.AOISystem{}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				aoi.Update(wa.sim, time.Millisecond)
			}
		})
		b.Run(tc.name+"/ai", func(b *testing.B) {
			ai := &systems.AISystem{}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ai.Update(wa.sim, time.Millisecond)
			}
		})
		b.Run(tc.name+"/move", func(b *testing.B) {
			mv := &systems.MoveSystem{}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				mv.Update(wa.sim, time.Millisecond)
			}
		})
	}
}

// aiSummary 规模场景的可比摘要（确定性断言用）。
type aiSummary struct {
	creatures int
	states    [4]int
	totalHP   int
	negHP     int
}

func summarizeAI(wa *WorldActor) aiSummary {
	var s aiSummary
	ecs.Query[components.Creature](wa.sim, func(e ecs.Entity, _ *components.Creature) {
		s.creatures++
		ai := ecs.Get[components.AI](wa.sim, e)
		s.states[int(ai.State)]++
		hp := ecs.Get[components.Health](wa.sim, e)
		s.totalHP += hp.Cur
		if hp.Cur < 0 {
			s.negHP++
		}
	})
	return s
}

// TestAIScale5000：5000 只生物跑 20 tick——
// 1) 确定性（两次运行摘要一致）；2) 出现追捕/攻击/逃跑（AI 条件满足）；
// 3) 血量不为负；4) 记录平均 tick 耗时。
func TestAIScale5000(t *testing.T) {
	if testing.Short() {
		t.Skip("scale test")
	}
	run := func() (aiSummary, time.Duration) {
		wa := buildAIWorld(t, 512, 512, 2500, 2500, 20)
		start := time.Now()
		for i := 0; i < 20; i++ {
			tickWorld(wa)
		}
		return summarizeAI(wa), time.Since(start) / 20
	}
	a, avgA := run()
	b, avgB := run()
	if a != b {
		t.Fatalf("两次运行摘要不一致: %+v vs %+v", a, b)
	}
	if a.creatures != 5000 {
		t.Fatalf("生物数 = %d, want 5000", a.creatures)
	}
	if a.states[1]+a.states[2] == 0 || a.states[3] == 0 {
		t.Fatalf("应出现追捕/攻击与逃跑: states=%v", a.states)
	}
	if a.negHP > 0 {
		t.Fatalf("存在负血量生物: %d", a.negHP)
	}
	t.Logf("5000 生物 20 tick 平均耗时: %v/tick（第一轮） / %v/tick（第二轮）", avgA, avgB)
}
