package behavior

import "testing"

// 回归：游荡**不能**把生物带出游荡半径。
//
// 这是行为树替换旧 idle 时真实引入的回归：
//   - 旧 AISystem.idle 每 tick 先判"是否超半径"，超了就走回出生点、
//     根本不进入游荡逻辑，所以生物永远出不了半径；
//   - 早期 WanderAction 把边界检查放在"换向周期"判断**之后**，
//     而那个周期条件大多数 tick 都不成立（函数直接 return），
//     于是边界检查几乎不执行。加上 Moveable 的方向会跨 tick 保留，
//     生物就沿对角一路冲出半径（实测走到 (14,14)，半径只有 6），
//     再被拉回、再冲出去，来回振荡。
//
// 断言：**每 tick 结束**时距离都不超过半径。
func TestWanderStaysWithinRoamRadius(t *testing.T) {
	const (
		radius = 6
		period = 24
		ticks  = 200
		homeX  = 10
		homeY  = 10
	)
	tree := NewTree(NewSelector(
		NewSequence(&OutOfRoam{}, &ReturnHomeAction{}),
		NewWander(period),
	))
	board := &fakeBoard{
		self: 7, roam: radius, homeX: homeX, homeY: homeY,
	}
	env := newFakeEnv()
	state := NewMemoryState()

	// 模拟"位置随移动意图变化"的世界：方向会被保留（与 Moveable 一致），
	// 否则测不出'方向持续导致越界'这个真实机制。
	posX, posY := homeX, homeY
	dirX, dirY := 0, 0
	for tick := 0; tick < ticks; tick++ {
		board.now = tick
		// 每 tick 把当前距离喂给黑板（HomeDistance 由 Env 提供）
		env.homeDist = abs(posX-homeX) + abs(posY-homeY)
		tree.Tick(NewTickContext(board, env, state, board.self))

		// 取本 tick 的移动意图（最后一条），模拟 Moveable 保留方向
		if n := len(env.moves); n > 0 {
			dirX, dirY = env.moves[n-1][0], env.moves[n-1][1]
			env.moves = env.moves[:0]
		}
		if n := env.homeCount; n > 0 {
			// MoveHome：朝出生点走一步
			dirX, dirY = sign(homeX-posX), sign(homeY-posY)
			env.homeCount = 0
		}
		posX += dirX
		posY += dirY

		dist := abs(posX-homeX) + abs(posY-homeY)
		if dist > radius {
			t.Fatalf("第 %d tick 游荡越界: pos=(%d,%d) dist=%d > 半径 %d",
				tick, posX, posY, dist, radius)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
