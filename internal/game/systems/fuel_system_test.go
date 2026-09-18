package systems

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// litCampfire 裸世界里的一个点燃火堆（只有 Fuel + HeatSource，不依赖地图/命令层）。
func litCampfire(w *ecs.World, cur int) (ecs.Entity, *components.Fuel) {
	e := w.CreateEntity()
	ecs.Add(w, e, components.Position{X: 4, Y: 4})
	f := components.Fuel{Cur: cur, Max: 1200, HeatStrength: 10, HeatRadius: 3}
	ecs.Add(w, e, f)
	components.RelightHeatSource(w, e, &f)
	return e, ecs.Get[components.Fuel](w, e)
}

// 点燃 → 消耗到 0 → HeatSource 被移除（熄灭）。
func TestFuelBurnoutExtinguishesHeatSource(t *testing.T) {
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)
	e, _ := litCampfire(w, 3)

	fs := &FuelSystem{}
	for i := 0; i < 3; i++ {
		fs.Update(w, 50*time.Millisecond)
	}

	if got := ecs.Get[components.Fuel](w, e).Cur; got != 0 {
		t.Fatalf("燃料应烧到 0，得到 %d", got)
	}
	if ecs.Has[components.HeatSource](w, e) {
		t.Fatal("燃料耗尽必须移除 HeatSource——客户端就是靠这一次组件移除看到火焰熄灭的")
	}
}

// 添柴 → 重新点燃：Cur 回到正数后 HeatSource 必须回来，且热量参数取自 Fuel。
func TestRefuelRelightsCampfire(t *testing.T) {
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)
	e, f := litCampfire(w, 1)

	fs := &FuelSystem{}
	fs.Update(w, 50*time.Millisecond) // 烧完，熄灭
	if ecs.Has[components.HeatSource](w, e) {
		t.Fatal("前置条件：这一幕应该已经熄灭")
	}

	// 命令层添柴只改 Fuel.Cur（世界层 refuel 就是这么做的）
	f.Cur += 1200
	ecs.MarkDirty[components.Fuel](w, e)
	fs.Update(w, 50*time.Millisecond)

	if got := ecs.Get[components.Fuel](w, e).Cur; got != 1199 {
		t.Fatalf("添柴后应继续燃烧，燃料 = %d, want 1199", got)
	}
	if !ecs.Has[components.HeatSource](w, e) {
		t.Fatal("添柴后必须重新点燃（HeatSource 回来），否则火堆再也点不着")
	}
	hs := ecs.Get[components.HeatSource](w, e)
	if hs.Strength != 10 || hs.Radius != 3 {
		t.Fatalf("复燃后的热量参数 = (%d,%d), want (10,3)——参数必须来自 Fuel 而不是复燃处的硬编码",
			hs.Strength, hs.Radius)
	}
}

// 燃烧中的火堆必须**每 tick** 标脏 Fuel（docs/P1.3 §11 同一契约）。
//
// 反例（"只在刚好归零时标脏"）会让快照里的剩余燃料长期停在旧值，
// 快照消费方（燃料条/HUD）看到的是一个几秒才跳一次的假进度。
func TestBurningFuelMarkedDirtyEveryTick(t *testing.T) {
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)
	litCampfire(w, 40)

	fuelID := ecs.ComponentIDOf[components.Fuel](w)
	fs := &FuelSystem{}
	const ticks = 30
	dirtyCount := 0
	for i := 0; i < ticks; i++ {
		w.DrainDirty()
		fs.Update(w, 50*time.Millisecond)
		for _, ids := range w.DrainDirty() {
			for _, id := range ids {
				if id == fuelID {
					dirtyCount++
				}
			}
		}
	}
	if dirtyCount != ticks {
		t.Fatalf("燃烧中必须每 tick 标脏 Fuel：%d/%d", dirtyCount, ticks)
	}
}

// 对照：熄灭后的火堆不该继续刷快照（Fuel 不变、HeatSource 已经不在，都是空操作）。
func TestExtinguishedCampfireNotMarkedDirty(t *testing.T) {
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)
	e, _ := litCampfire(w, 1)

	fs := &FuelSystem{}
	fs.Update(w, 50*time.Millisecond) // 烧完
	w.DrainDirty()

	fuelID := ecs.ComponentIDOf[components.Fuel](w)
	dirty := 0
	for i := 0; i < 20; i++ {
		fs.Update(w, 50*time.Millisecond)
		for _, ids := range w.DrainDirty() {
			for _, id := range ids {
				if id == fuelID {
					dirty++
				}
			}
		}
	}
	if dirty != 0 {
		t.Fatalf("熄灭的火堆不该继续标脏 Fuel：%d/20 次", dirty)
	}
	if ecs.Has[components.HeatSource](w, e) {
		t.Fatal("熄灭后 HeatSource 不该被重新挂上")
	}
}
