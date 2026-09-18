package world

import (
	"math"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// continuousPos 取"连续位置" = 整格 + 子格偏移（客户端和解比较用的就是这个量）。
func continuousPos(wa *WorldActor, e ecs.Entity) (float64, float64) {
	p := ecs.Get[components.Position](wa.sim, e)
	mv := ecs.Get[components.Moveable](wa.sim, e)
	return float64(p.X) + mv.SubX, float64(p.Y) + mv.SubY
}

// TestIdleOpDrivenPlayerDoesNotCoast：**操作驱动的玩家，本 tick 没消费到操作就一步都不走**。
//
// 这是"序号锚定和解"能够成立的前提。客户端拿"第 S 条操作之后我自己的状态"去比
// "服务端执行完第 S 条之后的状态" —— 只有服务端**每一步都由一条操作驱动**（K 条操作 = K 步）
// 时，这两个状态才是同一个东西。
//
// 曾经的实现：没有新操作的 tick 里，MoveSystem 仍然按"保留意图"跑 round 0
// （dirForRound → effectiveDir(mv) = 上次的输入方向）——对 AI/生物是对的，对操作驱动的玩家是错的：
// 服务端白走一步而 ACK 不动 ⇒ 服务端位置比"ACK 对应的状态"多走了一步。
// 实测量级：恒定偏 0.5 格（正好一个 tick 的位移），转向处翻倍到 1.0+，客户端于是每份快照都要
// 校正一次、偶尔超过 1.5 触发"直接贴" —— 就是"走得越久越卡"的根。
func TestIdleOpDrivenPlayerDoesNotCoast(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	wa.players[e] = "1"

	eng.Send(pid, BeginInputEpoch{UID: "1", Epoch: 9})
	eng.Send(pid, Command{UID: "1", InputEpoch: 9, Seq: 1, Kind: CommandMove, Data: MoveData{Entity: e, DX: 1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	x1, _ := continuousPos(wa, e)
	if x1 <= 0 {
		t.Fatalf("第 1 条操作应该真的走一步: x=%v", x1)
	}
	if got := wa.inputAcks["1"]; got != (InputAck{Epoch: 9, Seq: 1}) {
		t.Fatalf("ACK 应推进到 1: %+v", got)
	}

	// 这个 tick **没有任何操作**（包还在路上）：一步都不许走。
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	x2, _ := continuousPos(wa, e)
	if math.Abs(x2-x1) > 1e-9 {
		t.Fatalf("没有操作却走了：x %v → %v（服务端在拿保留意图白走，ACK 会对不上位置）", x1, x2)
	}
	if got := wa.inputAcks["1"]; got != (InputAck{Epoch: 9, Seq: 1}) {
		t.Fatalf("没有操作时 ACK 不该推进: %+v", got)
	}
	// 速度必须归零：留着上一 tick 的速度，别的客户端会拿它做外推（明明没走却滑出去）。
	if mv := ecs.Get[components.Moveable](wa.sim, e); mv.VelX != 0 || mv.VelY != 0 {
		t.Fatalf("没有操作时速度应归零: vel=(%v,%v)", mv.VelX, mv.VelY)
	}

	// 下一条操作到了：继续正好走一步（单调、可预期 —— 客户端重放才对得上）。
	eng.Send(pid, Command{UID: "1", InputEpoch: 9, Seq: 2, Kind: CommandMove, Data: MoveData{Entity: e, DX: 1}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)

	x3, _ := continuousPos(wa, e)
	if step := x3 - x1; math.Abs(step-0.5) > 1e-6 {
		t.Fatalf("一条操作应配一步（期望 +0.5）: x %v → %v（步长 %v）", x1, x3, step)
	}
	if got := wa.inputAcks["1"]; got != (InputAck{Epoch: 9, Seq: 2}) {
		t.Fatalf("ACK 应推进到 2: %+v", got)
	}
}

// TestNonOpDrivenEntityStillMovesEveryTick：反向保证 —— **不是操作驱动的实体（AI/生物/旧客户端）
// 仍然按 tick 自己走**。修"玩家不许白走"时不能把这条路堵死。
func TestNonOpDrivenEntityStillMovesEveryTick(t *testing.T) {
	eng, pid, wa := newTestWorld(t, WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	ecs.Add(wa.sim, e, components.Moveable{DirX: 1, Speed: 10})
	// 不进 wa.players、不发任何操作：纯 AI/生物语义（本 tick 无新操作 ⇒ 用 mv.Dir 跑 round 0）

	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	x1, _ := continuousPos(wa, e)
	if x1 <= 0 {
		t.Fatalf("无操作驱动的实体应当按保留方向继续走: x=%v", x1)
	}

	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	x2, _ := continuousPos(wa, e)
	if x2 <= x1 {
		t.Fatalf("无操作驱动的实体每个 tick 都要走: x %v → %v", x1, x2)
	}
}
