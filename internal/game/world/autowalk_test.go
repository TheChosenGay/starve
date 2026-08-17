package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/world/behavior"
)

// 空格兜底：交互距离内没有目标 → 把寻路结果压进移动队列走过去，走完即停（不自动执行）。
func TestAutomateWalksToTarget(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	// 手工地图：全平地（正式环境由 map.json 提供，寻路依赖 MapData）
	wa.sim.AddResource(&MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)})
	player := createPlayer(t, eng, pid, "u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	addBush(t, wa, 0, 3, 2) // 距离 3 > Picker.Range 1，但 AOI 半径 8 内

	eng.Send(pid, Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	eng.Send(pid, Tick{})
	// 行走途中再按一次空格：已在移动，不应重复压路（否则会走到目标后面）
	eng.Send(pid, Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	for i := 0; i < 30; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	pos := ecs.Get[components.Position](wa.sim, player)
	if pos.X != 0 || pos.Y != 3 {
		t.Fatalf("玩家应走到浆果 (0,3), got (%d,%d)", pos.X, pos.Y)
	}
	if mv := ecs.Get[components.Moveable](wa.sim, player); len(mv.Queue) != 0 {
		t.Fatalf("到达后移动队列应为空, queue=%v", mv.Queue)
	}
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 0 {
		t.Fatalf("走过去不应自动采集, berry = %d", inv.CountOf(components.ItemBerry))
	}
}

// 树/岩占格不可走：寻路到目标相邻的可走格，而不是往树上撞。
func TestAutomateWalksToBlockedTree(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	// 手工地图：全平地，Block 实体通过生命周期钩子写阻挡层
	wa.sim.AddResource(&MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)})
	player := createPlayer(t, eng, pid, "u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	equipTestAxe(wa, player)
	tree := wa.sim.CreateEntity()
	ecs.Add(wa.sim, tree, components.Position{X: 0, Y: 3})
	ecs.Add(wa.sim, tree, interactive.Choppable{Kind: components.ItemWood, WorkLeft: 3, MaxWork: 3})
	ecs.Add(wa.sim, tree, components.Block{Width: 1, Height: 1}) // 占格：写阻挡层

	eng.Send(pid, Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	for i := 0; i < 30; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	pos := ecs.Get[components.Position](wa.sim, player)
	if pos.X != 0 || pos.Y != 2 {
		t.Fatalf("应寻路到树旁 (0,2), got (%d,%d)", pos.X, pos.Y)
	}
	if got := ecs.Get[interactive.Choppable](wa.sim, tree).WorkLeft; got != 3 {
		t.Fatalf("走过去不应自动砍伐, WorkLeft = %d", got)
	}
}

// 按住空格（客户端持续发送 automate）：边走边重新就近评估——
// 走到交互范围内后自动执行，目标耗尽即停；无需 pending 状态（意图由持续按键携带）。
func TestAutomateHoldWalksThenExecutes(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	wa.sim.AddResource(&MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)})
	player := createPlayer(t, eng, pid, "u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	bush := addBush(t, wa, 0, 3, 2) // 距离 3 > Picker.Range 1；可采 2 次

	// 按住：每 3 tick 发一条 automate（≈ 6.7Hz），持续 60 tick
	for i := 0; i < 60; i++ {
		if i%3 == 0 {
			eng.Send(pid, Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
		}
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 2 {
		t.Fatalf("按住空格应把浆果采完, berry = %d, want 2", inv.CountOf(components.ItemBerry))
	}
	if got := ecs.Get[interactive.Pickable](wa.sim, bush).WorkLeft; got != 0 {
		t.Fatalf("浆果应采空, WorkLeft = %d", got)
	}
	pos := ecs.Get[components.Position](wa.sim, player)
	if d := absInt(pos.X) + absInt(pos.Y-3); d > 1 {
		t.Fatalf("执行后应停在浆果 1 格内, pos=(%d,%d)", pos.X, pos.Y)
	}
	if mv := ecs.Get[components.Moveable](wa.sim, player); len(mv.Queue) != 0 {
		t.Fatalf("执行后移动队列应为空, queue=%v", mv.Queue)
	}
}

// FindWalkTarget 兜底：交互距离内没有，但 AOI 内有匹配目标 → 返回该目标。
func TestFindWalkTargetFallback(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	bush := addBush(t, wa, 0, 5, 2) // 距离 5：超出 Picker.Range 1，AOI 8 内

	if intent, target, ok := behavior.FindBest(wa.sim, player, 8); ok {
		t.Fatalf("交互距离内不应找到目标, intent=%v target=%d", intent, target)
	}
	intent, target, ok := behavior.FindWalkTarget(wa.sim, player, 8)
	if !ok || intent != interactive.IntentPick || target != bush {
		t.Fatalf("FindWalkTarget = (intent=%v target=%d ok=%v), want (pick %d true)", intent, target, ok, bush)
	}
}
