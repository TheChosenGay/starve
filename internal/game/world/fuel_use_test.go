package world

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/config"
)

// newFuelWorld 载入真实模板表的世界：可燃物的燃料值来自 configs/resource_templates.json，
// 命令层测试必须走真配置，否则 wood 的 FuelTicks = 0，添柴分支永远不触发（测试会假绿）。
func newFuelWorld(t *testing.T) *WorldActor {
	t.Helper()
	gc, err := config.LoadGameConfig(WorldConfig{TemplatesPath: "../../../configs/resource_templates.json"})
	if err != nil {
		t.Fatal(err)
	}
	return NewWorldActorWithConfig(WorldConfig{}, gc)
}

// addCampfire 摆一个火堆（不走放置校验，聚焦燃料语义）；cur > 0 视为已点燃。
func addCampfire(wa *WorldActor, x, y, cur int) ecs.Entity {
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Building{
		Kind: components.BuildingCampfire, Width: 1, Height: 1, Placed: true,
	})
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	f := components.Fuel{
		Cur: cur, Max: campfireMaxFuelTicks,
		HeatStrength: campfireHeatStrength, HeatRadius: campfireHeatRadius,
	}
	ecs.Add(wa.sim, e, f)
	if cur > 0 {
		components.RelightHeatSource(wa.sim, e, &f)
	}
	return e
}

// useKind 直接走命令入口（与网关收包同一条路径）。
func useKind(wa *WorldActor, player ecs.Entity, kind components.ItemKind) {
	wa.cmds.Handle(Command{
		UID: "u1", Kind: CommandUse,
		Data: UseData{Player: player, Kind: kind},
	})
}

// 放置火堆：燃料与热量参数一起挂上，避免"放下就永久热源"（旧行为）。
func TestCampfirePlacementAttachesFuel(t *testing.T) {
	wa := newBuildingWorld(t)
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Building{Kind: components.BuildingCampfire, Width: 1, Height: 1})
	if !PlaceBuilding(wa.sim, e, 2, 2) {
		t.Fatal("放置应成功")
	}
	f := ecs.Get[components.Fuel](wa.sim, e)
	if f.Cur != campfireInitialFuelTicks || f.Max != campfireMaxFuelTicks {
		t.Fatalf("放置后的燃料 = %d/%d, want %d/%d",
			f.Cur, f.Max, campfireInitialFuelTicks, campfireMaxFuelTicks)
	}
	if f.HeatStrength != campfireHeatStrength || f.HeatRadius != campfireHeatRadius {
		t.Fatalf("燃料里必须存下热量参数（复燃要用）：%+v", f)
	}
	if !ecs.Has[components.HeatSource](wa.sim, e) {
		t.Fatal("刚放下的火堆应该是点着的（HeatSource 由 RelightHeatSource 挂上）")
	}
}

// 用可燃物 + 火堆在范围内 ⇒ 添柴：燃料增加、物品被消耗，
// 且**不会**落到"吃掉/使用效果"那条分支（用带 UseEffect 的木头验证）。
func TestUseFlammableNearCampfireRefuelsInsteadOfEating(t *testing.T) {
	wa := newFuelWorld(t)
	// 给木头临时加一条食用效果：只要添柴分支提前返回，饥饿就不该有任何变化。
	wood := wa.templates[components.ItemWood]
	if wood.FuelTicks <= 0 {
		t.Fatal("前置条件：wood 模板应有 fuel_ticks（配表没生效）")
	}
	wood.UseEffect = &UseEffect{Hunger: 50}
	wa.templates[components.ItemWood] = wood

	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})
	born := ecs.Get[components.Hunger](wa.sim, player).Level
	fire := addCampfire(wa, 2, 4, 100) // 距离 2 = 添柴范围
	inv := wa.cmds.ensureInventory(player)
	inv.Add(components.ItemWood, 2, wood.StackSize, 0)

	useKind(wa, player, components.ItemWood)

	if got := ecs.Get[components.Fuel](wa.sim, fire).Cur; got != 100+wood.FuelTicks {
		t.Fatalf("添柴后燃料 = %d, want %d", got, 100+wood.FuelTicks)
	}
	if got := inv.CountOf(components.ItemWood); got != 1 {
		t.Fatalf("添柴应消耗 1 个木头，背包剩 %d, want 1", got)
	}
	if got := ecs.Get[components.Hunger](wa.sim, player).Level; got != born {
		t.Fatalf("命中火堆就不该再走使用效果分支：饥饿 %d → %d", born, got)
	}
}

// 范围内没有火堆 ⇒ 退回原有 Use 语义（这里是"吃掉"），燃料与火堆状态不动。
func TestUseFlammableWithoutCampfireFallsBackToUseEffect(t *testing.T) {
	wa := newFuelWorld(t)
	wood := wa.templates[components.ItemWood]
	wood.UseEffect = &UseEffect{Hunger: 50}
	wa.templates[components.ItemWood] = wood

	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})
	hunger := 10
	ecs.Set(wa.sim, player, components.Hunger{Level: hunger, Rate: 0})
	fire := addCampfire(wa, 5, 5, 100) // 距离 6 > useRefuelRange
	inv := wa.cmds.ensureInventory(player)
	inv.Add(components.ItemWood, 1, wood.StackSize, 0)

	useKind(wa, player, components.ItemWood)

	if got := ecs.Get[components.Fuel](wa.sim, fire).Cur; got != 100 {
		t.Fatalf("超距不该添柴：燃料 = %d, want 100", got)
	}
	if got := ecs.Get[components.Hunger](wa.sim, player).Level; got != hunger+50 {
		t.Fatalf("没有火堆应退回使用效果：饥饿 = %d, want %d", got, hunger+50)
	}
	if got := inv.CountOf(components.ItemWood); got != 0 {
		t.Fatalf("走使用效果分支应消耗木头：剩 %d, want 0", got)
	}
}

// 不可燃物品不受影响：浆果在火堆边照样是"吃"。
func TestUseNonFlammableNearCampfireStillEats(t *testing.T) {
	wa := newFuelWorld(t)
	if wa.templates[components.ItemBerry].FuelTicks != 0 {
		t.Fatal("前置条件：浆果不该可燃")
	}
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})
	ecs.Set(wa.sim, player, components.Hunger{Level: 10, Rate: 0})
	fire := addCampfire(wa, 2, 4, 100)
	inv := wa.cmds.ensureInventory(player)
	inv.Add(components.ItemBerry, 1, 20, 0)

	useKind(wa, player, components.ItemBerry)

	if got := ecs.Get[components.Hunger](wa.sim, player).Level; got != 18 {
		t.Fatalf("浆果应在火堆边照常恢复饥饿：%d, want 18", got)
	}
	if got := ecs.Get[components.Fuel](wa.sim, fire).Cur; got != 100 {
		t.Fatalf("浆果不该改变燃料：%d, want 100", got)
	}
}

// 闭环：烧灭的火堆 → 添柴 → 下一 tick 复燃（命令只改 Cur，点燃由 FuelSystem 统一落笔）。
func TestUseFlammableRelightsBurnedOutCampfire(t *testing.T) {
	wa := newFuelWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})
	fire := addCampfire(wa, 2, 4, 0) // 已经烧灭
	inv := wa.cmds.ensureInventory(player)
	inv.Add(components.ItemWood, 1, wa.templates[components.ItemWood].StackSize, 0)

	useKind(wa, player, components.ItemWood)
	if ecs.Has[components.HeatSource](wa.sim, fire) {
		t.Fatal("命令层不该自己挂 HeatSource（点燃的唯一落笔点是 FuelSystem）")
	}
	tickWorld(wa)

	if !ecs.Has[components.HeatSource](wa.sim, fire) {
		t.Fatal("添柴后应复燃：HeatSource 回来，客户端火焰重新出现")
	}
	want := wa.templates[components.ItemWood].FuelTicks - 1 // 本 tick 已烧掉 1
	if got := ecs.Get[components.Fuel](wa.sim, fire).Cur; got != want {
		t.Fatalf("复燃后燃料 = %d, want %d", got, want)
	}
}

// 客户端契约：熄灭必须走增量快照的 **RemovedComponents 通道**（火焰消失全靠它），
// 而燃料值本身要在同一份 delta 里以新鲜数据出现（未来做燃料条也读得到）。
func TestExtinguishedCampfireGoesOutInDeltaSnapshot(t *testing.T) {
	wa := newFuelWorld(t)
	fire := addCampfire(wa, 4, 4, 1)
	wa.sim.DrainDirtySorted() // 丢掉装配期的脏标记

	wa.sim.RunSystems(50 * time.Millisecond) // 这一 tick 烧完 → 熄灭
	delta := DeltaSnapshot(wa.sim, wa.sim.DrainDirtySorted(), nil)

	heatRemoved := false
	for _, rc := range delta.RemovedComponents {
		if ecs.Entity(rc.EntityId) != fire {
			continue
		}
		for _, name := range rc.Components {
			if name == "HeatSource" {
				heatRemoved = true
			}
		}
	}
	if !heatRemoved {
		t.Fatal("熄灭必须以 RemovedComponents 下发 HeatSource——客户端的火焰表现就吃这条通道")
	}
	fuelFresh := false
	for _, st := range delta.Entities {
		if ecs.Entity(st.EntityId) != fire {
			continue
		}
		for _, cs := range st.Components {
			if cs.Component == "Fuel" {
				fuelFresh = true
			}
		}
	}
	if !fuelFresh {
		t.Fatal("燃料值应在同一份 delta 里以本 tick 的新值出现（每 tick 标脏）")
	}
}

// 添柴封顶：满燃料的火堆不消耗物品（柴不能白烧）。
func TestRefuelStopsAtMaxFuel(t *testing.T) {
	wa := newFuelWorld(t)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 2})
	fire := addCampfire(wa, 2, 4, campfireMaxFuelTicks)
	inv := wa.cmds.ensureInventory(player)
	inv.Add(components.ItemWood, 3, wa.templates[components.ItemWood].StackSize, 0)

	useKind(wa, player, components.ItemWood)

	if got := ecs.Get[components.Fuel](wa.sim, fire).Cur; got != campfireMaxFuelTicks {
		t.Fatalf("燃料不该超过上限：%d, want %d", got, campfireMaxFuelTicks)
	}
	if got := inv.CountOf(components.ItemWood); got != 3 {
		t.Fatalf("满燃料时不该消耗木头：剩 %d, want 3", got)
	}
}
