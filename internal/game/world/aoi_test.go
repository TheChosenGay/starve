package world

import (
	"testing"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// addAOIProbe 摆一个带感知组件的实体（radius 方形感知）。
func addAOIProbe(t *testing.T, wa *WorldActor, x, y, radius int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: 10, Max: 10})
	ecs.Add(wa.sim, e, components.AOI{Radius: radius})
	return e
}

// 方形感知：radius=2 覆盖 (2,0)，(3,0) 超出。
func TestAOIVisibleSquare(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AOIInterval: 1}) // 每 tick 重算，便于断言
	probe := addAOIProbe(t, wa, 0, 0, 2)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 0})

	tickWorld(wa)
	aoi := ecs.Get[components.AOI](wa.sim, probe)
	if len(aoi.Visible) != 1 || aoi.Visible[0] != player {
		t.Fatalf("radius=2 应感知 (2,0) 的玩家: %v", aoi.Visible)
	}

	ecs.Set(wa.sim, player, components.Position{X: 3, Y: 0})
	tickWorld(wa)
	if len(aoi.Visible) != 0 {
		t.Fatalf("radius=2 不应感知 (3,0): %v", aoi.Visible)
	}
}

// debug 开关：开 → Visible 进快照；关 → 只存 Radius。
func TestAOIDebugSnapshot(t *testing.T) {
	for _, debug := range []bool{true, false} {
		wa := NewWorldActor(WorldConfig{DebugAOI: debug, AOIInterval: 1})
		wolf := addWolf(t, wa, 0, 0)
		player := wa.createPlayer("u1")
		ecs.Set(wa.sim, player, components.Position{X: 1, Y: 0})
		tickWorld(wa)

		snap := FullSnapshot(wa.sim)
		found := false
		for _, es := range snap.Entities {
			if es.EntityId != uint64(wolf) {
				continue
			}
			for _, cs := range es.Components {
				if cs.Component != "AOI" {
					continue
				}
				found = true
				var aoi game.AOI
				if err := pb.Unmarshal(cs.Data, &aoi); err != nil {
					t.Fatal(err)
				}
				if aoi.Radius != 6 {
					t.Fatalf("radius = %d, want 6", aoi.Radius)
				}
				if debug && len(aoi.Visible) != 1 {
					t.Fatalf("debug 模式 Visible 应进快照: %v", aoi.Visible)
				}
				if !debug && len(aoi.Visible) != 0 {
					t.Fatalf("非 debug 模式 Visible 不应进快照: %v", aoi.Visible)
				}
			}
		}
		if !found {
			t.Fatalf("debug=%v 快照应含 AOI 组件", debug)
		}
	}
}
