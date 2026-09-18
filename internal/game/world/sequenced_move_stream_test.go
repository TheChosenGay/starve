package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/systems"
)

// TestBunchedSequencedMovesCatchUpInOneTick：同 tick 挤进来两条带序号的移动 → **一个 tick 内追两步**。
//
// 关键不变量：**K 条 Move 配 K 步移动积分**（绝不是"消费了不走"）。这里用
// "+X 然后 -X"构造：两条都真的走了 ⇒ 净位移 0；若只走了最后一条，净位移会是 -0.5。
func TestBunchedSequencedMovesCatchUpInOneTick(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	wa.players[e] = "1"

	eng.Send(pid, BeginInputEpoch{UID: "1", Epoch: 9})
	eng.Send(pid, Command{UID: "1", InputEpoch: 9, Seq: 1, Kind: CommandMove, Data: MoveData{Entity: e, DX: 1}})
	eng.Send(pid, Command{UID: "1", InputEpoch: 9, Seq: 2, Kind: CommandMove, Data: MoveData{Entity: e, DX: -1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	// 两条都被消费（ACK 到 2）
	if got := wa.inputAcks["1"]; got != (InputAck{Epoch: 9, Seq: 2}) {
		t.Fatalf("两条都应被消费: ACK=%+v", got)
	}
	// ★ K 条配 K 步：+0.5 再 -0.5 ⇒ 净位移 0（只走最后一条的话会是 -0.5）
	mv := ecs.Get[components.Moveable](wa.sim, e)
	if mv.SubX != 0 {
		t.Fatalf("两步都要真的走（+X 再 -X ⇒ 净 0）: subX=%v", mv.SubX)
	}
	if mv.DirX != -1 {
		t.Fatalf("生效方向应是最后一条: dir=%d", mv.DirX)
	}
}

// TestStepBudgetPacesTheMoveStream：把步数预算调成 1，就退回"每 tick 一条"的节奏
// （队列把多余的留到后面的 tick）——说明限速是可控的，不是靠"丢"。
func TestStepBudgetPacesTheMoveStream(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	ecs.Resource[systems.ControlQueue](wa.sim).StepBudget = 1
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	wa.players[e] = "1"

	eng.Send(pid, BeginInputEpoch{UID: "1", Epoch: 9})
	eng.Send(pid, Command{UID: "1", InputEpoch: 9, Seq: 1, Kind: CommandMove, Data: MoveData{Entity: e, DX: 1}})
	eng.Send(pid, Command{UID: "1", InputEpoch: 9, Seq: 2, Kind: CommandMove, Data: MoveData{Entity: e, DX: -1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	if got := wa.inputAcks["1"]; got.Seq != 1 {
		t.Fatalf("预算 1 时只该消费第 1 条: ACK=%+v", got)
	}
	if mv := ecs.Get[components.Moveable](wa.sim, e); mv.SubX != 0.5 && mv.SubX != -0.5 {
		t.Fatalf("第 1 条应生效: subX=%v", mv.SubX)
	}

	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	if got := wa.inputAcks["1"]; got.Seq != 2 {
		t.Fatalf("第 2 条应在下一个 tick 被消费: ACK=%+v", got)
	}
}

// TestEachPlayerHasItsOwnMoveStream：多人时**每人一条队列**，每 tick 每人各消费一条，互不影响。
// （服务端不会把所有人的操作串成一条队列；代价是每 tick O(玩家数) 次移动应用。）
func TestEachPlayerHasItsOwnMoveStream(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	p1 := wa.createPlayer("u1")
	p2 := wa.createPlayer("u2")

	// 两个玩家各发两条带序号的操作，且都挤在同一个 tick 里
	eng.Send(pid, BeginInputEpoch{UID: "u1", Epoch: 1})
	eng.Send(pid, BeginInputEpoch{UID: "u2", Epoch: 1})
	eng.Send(pid, Command{UID: "u1", InputEpoch: 1, Seq: 1, Kind: CommandMove, Data: MoveData{Entity: p1, DX: 1}})
	eng.Send(pid, Command{UID: "u1", InputEpoch: 1, Seq: 2, Kind: CommandMove, Data: MoveData{Entity: p1, DX: -1}})
	eng.Send(pid, Command{UID: "u2", InputEpoch: 1, Seq: 1, Kind: CommandMove, Data: MoveData{Entity: p2, DX: 1}})
	eng.Send(pid, Command{UID: "u2", InputEpoch: 1, Seq: 2, Kind: CommandMove, Data: MoveData{Entity: p2, DX: -1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	// 两个玩家各自在同一个 tick 里追两步（+X 再 -X ⇒ 净 0），互不影响
	for _, p := range []ecs.Entity{p1, p2} {
		mv := ecs.Get[components.Moveable](wa.sim, p)
		if mv.SubX != 0 {
			t.Fatalf("每个玩家都应走完两步（净 0）: subX=%v", mv.SubX)
		}
		if mv.DirX != -1 {
			t.Fatalf("生效方向应是各自最后一条: dir=%d", mv.DirX)
		}
	}
	if got := wa.inputAcks["u1"]; got.Seq != 2 {
		t.Fatalf("u1 ACK=%d want 2", got.Seq)
	}
	if got := wa.inputAcks["u2"]; got.Seq != 2 {
		t.Fatalf("u2 ACK=%d want 2", got.Seq)
	}
}

// TestRedundantAndOutOfOrderOpsAreDeduped：冗余重发 + 乱序补齐都必须被正确吸收。
//
// 客户端每 tick 会把**未确认的操作再发一遍**（丢包不丢输入），所以服务端会收到重复包；
// 网络还可能让包乱序到达。这里模拟：seq 3 先到、seq 1/2 随后补上（且 seq 2 发了两遍）。
func TestRedundantAndOutOfOrderOpsAreDeduped(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	wa.players[e] = "1"

	eng.Send(pid, BeginInputEpoch{UID: "1", Epoch: 5})
	// 乱序：先 seq 3，再补 1、2；seq 2 冗余重发一次
	eng.Send(pid, Command{UID: "1", InputEpoch: 5, Seq: 3, Kind: CommandMove, Data: MoveData{Entity: e, DX: 1}})
	eng.Send(pid, Command{UID: "1", InputEpoch: 5, Seq: 1, Kind: CommandMove, Data: MoveData{Entity: e, DX: 1}})
	eng.Send(pid, Command{UID: "1", InputEpoch: 5, Seq: 2, Kind: CommandMove, Data: MoveData{Entity: e, DX: 1}})
	eng.Send(pid, Command{UID: "1", InputEpoch: 5, Seq: 2, Kind: CommandMove, Data: MoveData{Entity: e, DX: -1}}) // 重复包（内容改了也不该生效）
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	// 三条按 seq 顺序各跑一步：+0.5 ×3 = 1.5（重复的那条被丢掉，不能变成 4 步）
	mv := ecs.Get[components.Moveable](wa.sim, e)
	pos := ecs.Get[components.Position](wa.sim, e)
	got := float64(pos.X) + mv.SubX
	if got != 1.5 {
		t.Fatalf("乱序+冗余应三步步进 1.5 格（不是 4 步、也不是乱序）: got=%v", got)
	}
	if got := wa.inputAcks["1"]; got.Seq != 3 {
		t.Fatalf("ACK 应到 3: %+v", got)
	}
}
