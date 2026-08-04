package ecs_test

import (
	"fmt"
	"time"

	"starve/internal/ecs"
)

type Health struct{ Max, Cur int }
type Hunger struct{ Level int }

type hungerSystem struct{ rate int }

func (s *hungerSystem) Update(w *ecs.World, dt time.Duration) {
	ecs.Query[Hunger](w, func(e ecs.Entity, h *Hunger) {
		h.Level -= s.rate
	})
}

func ExampleWorld() {
	w := ecs.NewWorld()

	player := w.CreateEntity()
	ecs.Add(w, player, Health{Max: 100, Cur: 100})
	ecs.Add(w, player, Hunger{Level: 100})

	tree := w.CreateEntity()
	ecs.Add(w, tree, Health{Max: 50, Cur: 50})

	w.AddSystem(10, &hungerSystem{rate: 1})
	w.RunSystems(100 * time.Millisecond)

	// 只有玩家同时拥有 Health 和 Hunger
	ecs.Query2[Health, Hunger](w, func(e ecs.Entity, hp *Health, h *Hunger) {
		fmt.Printf("entity %d: hp=%d hunger=%d\n", e, hp.Cur, h.Level)
	})

	// Output:
	// entity 1: hp=100 hunger=99
}
