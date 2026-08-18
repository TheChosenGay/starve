package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// addLoot 在坐标摆一个可拾取掉落物（Lootable）。
func addLoot(t *testing.T, wa *WorldActor, x, y int, kind components.ItemKind, count int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Lootable{Items: []components.ItemStack{{Kind: kind, Count: count}}})
	return e
}

// 拾取命令：Looter ↔ Lootable 匹配成功 → 物品入包 + 掉落物销毁。
func TestPickupBehavior(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	loot := addLoot(t, wa, 0, 1, components.ItemBerry, 3) // 距离 1 ≤ Looter.Range 2

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: loot}})

	if wa.sim.IsAlive(loot) {
		t.Fatal("拾取后掉落物应销毁")
	}
	if inv := ecs.Get[components.Inventory](wa.sim, player); inv.CountOf(components.ItemBerry) != 3 {
		t.Fatalf("背包 berry = %d, want 3", inv.CountOf(components.ItemBerry))
	}
}

// 超出 Looter.Range 不拾取。
func TestPickupOutOfRange(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	loot := addLoot(t, wa, 0, 3, components.ItemBerry, 3) // 距离 3 > Looter.Range 2

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: loot}})

	if !wa.sim.IsAlive(loot) {
		t.Fatal("超出范围不应拾取")
	}
	if inv := ecs.Get[components.Inventory](wa.sim, player); inv.CountOf(components.ItemBerry) != 0 {
		t.Fatalf("超出范围背包不应有物品, berry = %d", inv.CountOf(components.ItemBerry))
	}
}

// 空格自动拾取：Lootable 是自动行为候选，范围内按距离就近拾取。
func TestAutomatePicksUpLoot(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	wa.sim.AddResource(&MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)})
	player := createPlayer(t, eng, pid, "u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	loot := addLoot(t, wa, 0, 1, components.ItemWood, 2)

	eng.Send(pid, Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if wa.sim.IsAlive(loot) {
		t.Fatal("空格应自动拾取并销毁掉落物")
	}
	if inv := ecs.Get[components.Inventory](wa.sim, player); inv.CountOf(components.ItemWood) != 2 {
		t.Fatalf("背包 wood = %d, want 2", inv.CountOf(components.ItemWood))
	}
}

// 旧档迁移：Loot → Lootable（拾取纳入 -er/-able 行为体系）。
func TestMigrateLoot(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 1, Y: 1})
	ecs.Add(wa.sim, e, components.Loot{Items: []components.ItemStack{{Kind: components.ItemBerry, Count: 2}}})

	wa.migrateLoot()

	if !ecs.Has[components.Lootable](wa.sim, e) || ecs.Has[components.Loot](wa.sim, e) {
		t.Fatal("Loot 应迁移为 Lootable 并移除 Loot")
	}
	if got := ecs.Get[components.Lootable](wa.sim, e).Items[0].Count; got != 2 {
		t.Fatalf("迁移后物品数量 = %d, want 2", got)
	}
}
