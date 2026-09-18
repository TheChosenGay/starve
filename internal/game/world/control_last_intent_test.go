package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	game "starve/pkg/proto/game"
)

// 本文件记录的是**带序号（客户端）操作的仲裁策略**。
//
// 策略在 2026-02 换过：
//
//	旧：同一个 tick 里对每个 Actor 只执行"最后到达的那一条意图"，其余 Superseded 丢弃。
//	    那是为"客户端只在意图变化时才发、且不保证有序"的年代写的兜底。
//	新：**所有带序号的操作共用一条队列，按 seq 顺序排队，每 tick 消费一条**（积压时追步）。
//	    因为客户端现在每个 tick 采样一条编号操作，同一个 tick 里挤进多条只可能是网络抖动 ——
//	    这些操作在客户端本来就是不同 tick 上的，按序应用才忠实于玩家的操作流；
//	    丢掉前面的会让客户端"第 N 条操作之后的状态"和权威对不上（和解出现假失配）。
//
// 无序号（Seq == 0）的意图 —— AI/服务端直接调用/旧客户端 —— 仍保持"最后一条赢、立即生效"。
func TestSequencedOpsAreAppliedInSeqOrder(t *testing.T) {
	t.Run("attack then move", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		target := addActionTarget(wa, 0, 1, 100)
		components.BeginTickEvents(wa.sim, 1)

		wa.cmds.Handle(Command{
			UID: "u1", Seq: 1, RequestID: 101, Kind: CommandAttack,
			Data: AttackData{Attacker: player, Target: target},
		})
		wa.cmds.Handle(Command{
			UID: "u1", Seq: 2, Kind: CommandMove,
			Data: MoveData{Entity: player, DX: 1},
		})
		tickWorld(wa)

		// 两条都按 seq 顺序应用：攻击先起手，移动随后（移动会打断攻击）。
		// 注意只有 Move 占"步数预算"，Action 不占 —— 所以动作不会因为步数预算被推迟。
		results := ecs.Resource[systems.ControlQueue](wa.sim).Results
		if len(results) != 2 || !results[0].Accepted || !results[1].Accepted {
			t.Fatalf("results=%+v", results)
		}
		if mv := ecs.Get[components.Moveable](wa.sim, player); mv.DirX != 1 || mv.DirY != 0 {
			t.Fatalf("第 2 条（移动）方向应生效: dir=(%d,%d)", mv.DirX, mv.DirY)
		}
	})

	t.Run("move then attack", func(t *testing.T) {
		wa := NewWorldActor(WorldConfig{})
		player := wa.createPlayer("u1")
		target := addActionTarget(wa, 0, 1, 100)
		components.BeginTickEvents(wa.sim, 1)

		wa.cmds.Handle(Command{
			UID: "u1", Seq: 1, Kind: CommandMove,
			Data: MoveData{Entity: player, DX: 1},
		})
		wa.cmds.Handle(Command{
			UID: "u1", Seq: 2, RequestID: 202, Kind: CommandAttack,
			Data: AttackData{Attacker: player, Target: target},
		})
		tickWorld(wa)

		results := ecs.Resource[systems.ControlQueue](wa.sim).Results
		if len(results) != 2 || !results[0].Accepted || !results[1].Accepted {
			t.Fatalf("results=%+v", results)
		}
		// 第 2 条（攻击）随后起手。注意：起手动作会把移动方向清掉（游戏设计：动作打断移动），
		// 这里只断言"两条都按顺序被消费、攻击确实起手了"。
		state := ecs.Get[components.ActionState](wa.sim, player)
		if state.TargetEntity != target || state.RequestID != 202 {
			t.Fatalf("action=%+v", state)
		}
	})
}

// 两条带序号的攻击：按 seq 顺序，第 1 条生效、第 2 条因 Busy 被拒（而不是"最后一条赢"丢掉第 1 条）。
func TestSequencedActionsQueueAndSecondIsRejectedWhileBusy(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	first := addActionTarget(wa, 0, 1, 100)
	last := addActionTarget(wa, 1, 0, 100)

	components.BeginTickEvents(wa.sim, 1)
	wa.cmds.Handle(Command{
		UID: "u1", Seq: 1, RequestID: 101, Kind: CommandAttack,
		Data: AttackData{Attacker: player, Target: first},
	})
	wa.cmds.Handle(Command{
		UID: "u1", Seq: 2, RequestID: 202, Kind: CommandAttack,
		Data: AttackData{Attacker: player, Target: last},
	})
	tickWorld(wa)

	results := ecs.Resource[systems.ControlQueue](wa.sim).Results
	if len(results) != 2 || !results[0].Accepted || results[1].Accepted ||
		results[1].Reason != systems.ControlRejectedBusy {
		t.Fatalf("第 1 条生效、第 2 条 Busy 被拒: %+v", results)
	}
	if got := ecs.Get[components.ActionState](wa.sim, player).TargetEntity; got != first {
		t.Fatalf("第 1 条应先生效: target=%d want %d", got, first)
	}
	events := components.DrainTickEvents(wa.sim)
	if len(events) != 1 || events[0].GetOutcome() == nil ||
		events[0].GetOutcome().RequestId != 202 ||
		events[0].GetOutcome().Result != game.ActionOutcomeResult_ACTION_OUTCOME_RESULT_REJECTED {
		t.Fatalf("第 2 条应被 Busy 拒绝并回执: %+v", events)
	}
}

// 移动 + 取消：同一个 tick 里按 seq 顺序消费（Move 先、Cancel 后），顺序不能反。
func TestSequencedMoveAndCancelApplyInOrder(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	target := addActionTarget(wa, 0, 1, 100)

	components.BeginTickEvents(wa.sim, 1)
	wa.cmds.Handle(Command{
		UID: "u1", Seq: 1, RequestID: 11, Kind: CommandAttack,
		Data: AttackData{Attacker: player, Target: target},
	})
	tickWorld(wa)
	components.DrainTickEvents(wa.sim)

	wa.cmds.Handle(Command{
		UID: "u1", Seq: 2, Kind: CommandMove,
		Data: MoveData{Entity: player, DX: 1},
	})
	wa.cmds.Handle(Command{
		UID: "u1", Seq: 3, Kind: CommandCancelCraft,
		Data: CancelCraftData{Player: player},
	})
	tickWorld(wa)

	results := ecs.Resource[systems.ControlQueue](wa.sim).Results
	if len(results) != 2 || !results[0].Accepted {
		t.Fatalf("cancel results=%+v", results)
	}
	if results[1].Pending || results[1].Superseded {
		t.Fatalf("取消应按 seq 顺序在本 tick 被消费（不排队、不丢弃）: %+v", results[1])
	}
	if mv := ecs.Get[components.Moveable](wa.sim, player); mv.DirX != 1 {
		t.Fatalf("第 2 条（移动）方向应生效: dir=%d", mv.DirX)
	}
}
