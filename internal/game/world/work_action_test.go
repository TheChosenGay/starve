package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

func TestWorkActionsCommitOnTimeline(t *testing.T) {
	tests := []struct {
		name       string
		kind       components.ActionKind
		command    func(ecs.Entity, ecs.Entity) Command
		addAbility func(*WorldActor, ecs.Entity)
		addTarget  func(*WorldActor, ecs.Entity)
		workLeft   func(*WorldActor, ecs.Entity) int
	}{
		{
			name: "chop", kind: components.ActionChop,
			command: func(actor, target ecs.Entity) Command {
				return Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: actor, Target: target}}
			},
			addAbility: func(wa *WorldActor, actor ecs.Entity) {
				ecs.Add(wa.sim, actor, interactive.Chopper{Efficiency: 1, Range: 2, Durability: -1})
			},
			addTarget: func(wa *WorldActor, target ecs.Entity) {
				ecs.Add(wa.sim, target, interactive.Choppable{Kind: components.ItemWood, WorkLeft: 2, MaxWork: 2})
			},
			workLeft: func(wa *WorldActor, target ecs.Entity) int {
				return ecs.Get[interactive.Choppable](wa.sim, target).WorkLeft
			},
		},
		{
			name: "mine", kind: components.ActionMine,
			command: func(actor, target ecs.Entity) Command {
				return Command{UID: "u1", Kind: CommandMine, Data: MineData{Player: actor, Target: target}}
			},
			addAbility: func(wa *WorldActor, actor ecs.Entity) {
				ecs.Add(wa.sim, actor, interactive.Miner{Efficiency: 1, Range: 2, Durability: -1})
			},
			addTarget: func(wa *WorldActor, target ecs.Entity) {
				ecs.Add(wa.sim, target, interactive.Minable{Kind: components.ItemFlint, WorkLeft: 2, MaxWork: 2})
			},
			workLeft: func(wa *WorldActor, target ecs.Entity) int {
				return ecs.Get[interactive.Minable](wa.sim, target).WorkLeft
			},
		},
		{
			name: "pick", kind: components.ActionPick,
			command: func(actor, target ecs.Entity) Command {
				return Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: actor, Target: target}}
			},
			addAbility: func(wa *WorldActor, actor ecs.Entity) {},
			addTarget: func(wa *WorldActor, target ecs.Entity) {
				ecs.Add(wa.sim, target, interactive.Pickable{Kind: components.ItemBerry, WorkLeft: 2, MaxWork: 2})
			},
			workLeft: func(wa *WorldActor, target ecs.Entity) int {
				return ecs.Get[interactive.Pickable](wa.sim, target).WorkLeft
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wa := NewWorldActor(WorldConfig{})
			actor := wa.createPlayer("u1")
			target := wa.sim.CreateEntity()
			ecs.Add(wa.sim, target, components.Position{X: 0, Y: 1})
			tt.addAbility(wa, actor)
			tt.addTarget(wa, target)

			wa.cmds.Handle(tt.command(actor, target))
			tickWorld(wa)
			state := ecs.Get[components.ActionState](wa.sim, actor)
			if state.Kind != tt.kind {
				t.Fatalf("kind=%v, want %v", state.Kind, tt.kind)
			}
			runActionTicks(wa, 3)
			if got := tt.workLeft(wa, target); got != 2 {
				t.Fatalf("commit 前 WorkLeft=%d, want 2", got)
			}
			tickWorld(wa)
			if got := tt.workLeft(wa, target); got != 1 {
				t.Fatalf("commit 后 WorkLeft=%d, want 1", got)
			}
			if tt.kind == components.ActionPick {
				if got := ecs.Get[components.Inventory](wa.sim, actor).CountOf(components.ItemBerry); got != 1 {
					t.Fatalf("Pick commit 后 berry=%d, want 1", got)
				}
			}
		})
	}
}

func TestWorkMoveAndDamageCancellation(t *testing.T) {
	t.Run("move", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{MoveSpeed: 20})
		player := wa.createPlayer("u1")
		bush := addBush(t, wa, 0, 1, 2)
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: player, Target: bush}})
		tickWorld(wa)
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: player, DX: 1}})
		tickWorld(wa)
		runActionTicks(wa, 4)
		if ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("移动后动作未取消")
		}
		if got := ecs.Get[interactive.Pickable](wa.sim, bush).WorkLeft; got != 2 {
			t.Fatalf("移动取消后 WorkLeft=%d, want 2", got)
		}
	})

	t.Run("damage", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		attacker := wa.createPlayer("u2")
		bush := addBush(t, wa, 0, 1, 2)

		wa.cmds.Handle(Command{UID: "u2", Kind: CommandAttack, Data: AttackData{Attacker: attacker, Target: player}})
		tickWorld(wa)
		runActionTicks(wa, 5)
		wa.cmds.Handle(Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: player, Target: bush}})
		tickWorld(wa)
		runActionTicks(wa, 2)

		if ecs.Has[components.ActionState](wa.sim, player) {
			t.Fatal("受击后工作动作未取消")
		}
		if got := ecs.Get[interactive.Pickable](wa.sim, bush).WorkLeft; got != 2 {
			t.Fatalf("受击取消后 WorkLeft=%d, want 2", got)
		}
	})
}

func TestWorkCommitPreservesToolBreakAndDrops(t *testing.T) {
	wa := NewWorldActor(testM5Cfg(t))
	player := wa.createPlayer("u1")
	tree := findWorkable(t, wa, components.ItemWood)
	ecs.Set(wa.sim, player, components.Position{X: 2, Y: 0})

	tool := wa.sim.CreateEntity()
	ecs.Add(wa.sim, tool, interactive.Equipment{Kind: components.ItemAxe})
	ecs.Add(wa.sim, tool, interactive.Chopper{Efficiency: 10, Range: 2, Durability: 1})
	equip := ecs.Ensure[components.Equip](wa.sim, player)
	equip.Set(components.SlotHand, tool)
	ecs.Add(wa.sim, player, interactive.Chopper{Efficiency: 10, Range: 2, Durability: -1})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandChop, Data: ChopData{Player: player, Target: tree}})
	runActionTicks(wa, 5)
	wa.processDrops()

	if ecs.Get[components.Equip](wa.sim, player).Item(components.SlotHand) != 0 ||
		ecs.Has[interactive.Chopper](wa.sim, player) {
		t.Fatal("工具耗尽后应自动卸下")
	}
	if wa.sim.IsAlive(tree) {
		t.Fatal("Chop commit 后资源来源应销毁")
	}
	if findLootableKind(t, wa, components.ItemWood) == tree {
		t.Fatal("Chop commit 后 Dead/drop 流程退化")
	}
}
