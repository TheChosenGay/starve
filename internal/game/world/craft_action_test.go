package world

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

func giveAxeIngredients(wa *WorldActor, player ecs.Entity) {
	inv := ecs.Get[components.Inventory](wa.sim, player)
	inv.Add(components.ItemWood, 3, 20, 0)
	inv.Add(components.ItemFlint, 1, 20, 0)
}

func TestCraftActionTimelineAndNormalCompletion(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, testM5Cfg(t))
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	giveAxeIngredients(wa, player)

	resp := eng.Request(pid, CraftRequest{UID: "u1", RecipeID: "axe"}, time.Second)
	value, err := resp.Wait()
	if err != nil || !value.(CraftResult).Started {
		t.Fatalf("craft 未开始: value=%v err=%v", value, err)
	}
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if !ecs.Has[components.ActionState](wa.sim, player) || !ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("Craft 开始后必须同时存在 ActionState 与 Crafting")
	}
	action := ecs.Get[components.ActionState](wa.sim, player)
	if action.Kind != components.ActionCraft || action.CommitTick-action.PhaseStartTick != 3 {
		t.Fatalf("Craft 时间线错误: kind=%v start=%d commit=%d", action.Kind, action.PhaseStartTick, action.CommitTick)
	}
	if got := ecs.Get[components.Crafting](wa.sim, player).TicksLeft; got != 3 {
		t.Fatalf("初始 TicksLeft=%d, want 3", got)
	}

	for i := 0; i < 2; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemAxe); got != 0 {
		t.Fatalf("commit 前产出 axe=%d", got)
	}

	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemAxe); got != 1 {
		t.Fatalf("正常完成 axe=%d, want 1", got)
	}
	if ecs.Has[components.ActionState](wa.sim, player) || ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("正常完成后 ActionState/Crafting 应同时移除")
	}
	done := 0
	for _, effect := range pushed() {
		if effect.Route == proto.RouteCraftDone {
			if craftDone, ok := effect.Payload.(*proto.CraftDone); ok && craftDone.Success {
				done++
			}
		}
	}
	if done != 1 {
		t.Fatalf("成功 CraftDone 次数=%d, want 1", done)
	}
}

func TestTryInterruptClearsCraftActionOnce(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Add(wa.sim, player, components.Crafting{
		RecipeID: "axe",
		Ingredients: []components.ItemStack{
			{Kind: components.ItemWood, Count: 3, MaxStack: 20},
		},
	})
	ecs.Add(wa.sim, player, components.ActionState{
		ActionID: 1, Kind: components.ActionCraft, Phase: components.ActionWindup, CommitTick: 10, EndTick: 10,
	})

	if !components.TryInterrupt(
		wa.sim, player,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT,
	) {
		t.Fatal("应命中可打断组件")
	}
	if ecs.Has[components.ActionState](wa.sim, player) || ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("单次中断必须完整移除 ActionState 与 Crafting")
	}
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWood); got != 3 {
		t.Fatalf("首次退款 wood=%d, want 3", got)
	}
	effects := wa.sim.DrainEffects()
	craftDone := 0
	for _, effect := range effects {
		if _, ok := effect.(*proto.CraftDone); ok {
			craftDone++
		}
	}
	events := components.DrainTickEvents(wa.sim)
	if craftDone != 1 || len(events) != 1 || events[0].GetOutcome() == nil {
		t.Fatalf("取消 CraftDone/events=(%d,%+v), want 1/Outcome", craftDone, events)
	}

	if components.TryInterrupt(
		wa.sim, player,
		game.ActionOutcomeReason_ACTION_OUTCOME_REASON_EXPLICIT,
	) {
		t.Fatal("重复中断不应再次命中")
	}
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWood); got != 3 {
		t.Fatalf("重复中断后 wood=%d, want 3", got)
	}
	if effects := wa.sim.DrainEffects(); len(effects) != 0 {
		t.Fatalf("重复中断通知数=%d, want 0", len(effects))
	}
	if events := components.DrainTickEvents(wa.sim); len(events) != 0 {
		t.Fatalf("重复中断领域事件数=%d, want 0", len(events))
	}
}

func TestDeathInterruptsCraftAction(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	ecs.Get[components.Health](wa.sim, player).Cur = 0
	ecs.Add(wa.sim, player, components.Crafting{
		RecipeID: "axe",
		Ingredients: []components.ItemStack{
			{Kind: components.ItemWood, Count: 3, MaxStack: 20},
		},
	})
	ecs.Add(wa.sim, player, components.ActionState{
		ActionID: 1, Kind: components.ActionCraft, Phase: components.ActionWindup, CommitTick: 10, EndTick: 10,
	})

	(&systems.DeathSystem{}).Update(wa.sim, 0)
	if !ecs.Has[components.Dead](wa.sim, player) {
		t.Fatal("死亡系统未标记 Dead")
	}
	if ecs.Has[components.ActionState](wa.sim, player) || ecs.Has[components.Crafting](wa.sim, player) {
		t.Fatal("死亡必须同时清理制作动作与数据")
	}
	if got := ecs.Get[components.Inventory](wa.sim, player).CountOf(components.ItemWood); got != 3 {
		t.Fatalf("死亡退款 wood=%d, want 3", got)
	}
}
