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
