package worldmap

// FleeDir 逃跑方向：远离威胁点（avoid），并校验目标格可走（WalkGrid、不出界）。
// 依次尝试"完全反向 + 45° 旋转"的候选，取第一个可走的；都不可走返回 (0,0)（原地）。
func FleeDir(g *WalkGrid, x, y, avoidX, avoidY int) (int, int) {
	dx, dy := 0, 0
	if x != avoidX || y != avoidY {
		dx, dy = signStep(x-avoidX), signStep(y-avoidY)
	}
	candidates := [][2]int{
		{dx, dy}, {dy, -dx}, {-dy, dx}, {dx, -dy},
		{-dx, dy}, {-dx, -dy}, {dy, dx}, {-dy, -dx},
	}
	for _, c := range candidates {
		if c[0] == 0 && c[1] == 0 {
			continue
		}
		nx, ny := x+c[0], y+c[1]
		if g == nil {
			return c[0], c[1]
		}
		if g.Walkable(nx, ny) {
			return c[0], c[1]
		}
	}
	return 0, 0
}

func signStep(v int) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}
