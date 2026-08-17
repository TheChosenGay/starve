package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/world/behavior"
)

// 空格兜底：交互距离内没有目标 → 挂 AutoWalk 自动走过去，进入范围后自动采集。
func TestAutomateWalksToTarget(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	bush := addBush(t, wa, 0, 3, 2) // 距离 3 > Picker.Range 1，但 AOI 半径 8 内

	eng.Send(pid, Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	for i := 0; i < 30; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if ecs.Has[components.AutoWalk](wa.sim, player) {
		t.Fatal("采集完成后 AutoWalk 应被移除")
	}
	pos := ecs.Get[components.Position](wa.sim, player)
	if d := absInt(pos.X) + absInt(pos.Y-3); d > 1 {
		t.Fatalf("玩家应在浆果 1 格内, pos=(%d,%d)", pos.X, pos.Y)
	}
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 1 {
		t.Fatalf("背包 berry = %d, want 1", inv.CountOf(components.ItemBerry))
	}
	if got := ecs.Get[interactive.Pickable](wa.sim, bush).WorkLeft; got != 1 {
		t.Fatalf("浆果 WorkLeft = %d, want 1（自动采集一次）", got)
	}
}

// 自动行走途中玩家手动移动 → 取消 AutoWalk，不再自动采集。
func TestAutomateWalkCanceledByManualMove(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	addBush(t, wa, 0, 5, 2)

	eng.Send(pid, Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	eng.Send(pid, Tick{})
	// 手动接管方向（反向走开）
	eng.Send(pid, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 0, DY: -1}})
	for i := 0; i < 30; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if ecs.Has[components.AutoWalk](wa.sim, player) {
		t.Fatal("手动移动应取消 AutoWalk")
	}
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemBerry) != 0 {
		t.Fatalf("手动接管后不应自动采集, berry = %d", inv.CountOf(components.ItemBerry))
	}
}

// 自动行走途中目标消失 → AutoWalk 取消，不 panic。
func TestAutomateWalkTargetGone(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	bush := addBush(t, wa, 0, 5, 2)

	eng.Send(pid, Command{UID: "u1", Kind: CommandAutomate, Data: AutomateData{Player: player}})
	eng.Send(pid, Tick{})
	wa.sim.DestroyEntity(bush) // 目标被移除（他人砍走/服务器清理）
	for i := 0; i < 10; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if ecs.Has[components.AutoWalk](wa.sim, player) {
		t.Fatal("目标消失后 AutoWalk 应取消")
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

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
