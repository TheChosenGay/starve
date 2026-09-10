package main

import (
	"math"
	"testing"
)

// TestArenaChaseAndWander：目标漫游不出界；玩家朝最近目标追、并在 reach 附近停下。
func TestArenaChaseAndWander(t *testing.T) {
	if !arenaSetup(arenaSetupIn{
		Player:  arenaBody{X: -6, Z: 0, Dir: 0},
		Targets: []arenaBody{{X: 6, Z: 0, Dir: 0, Speed: 1}},
		Arena:   8,
	}) {
		t.Fatal("setup 应当成功")
	}
	out := arenaStep(arenaStepIn{Dt: 1.0 / 60, Chase: true, Speed: 1.1, Reach: 1.5})
	if out.PlayerX <= -6 {
		t.Fatalf("玩家应当朝目标移动，实际 X=%v", out.PlayerX)
	}
	if math.Abs(out.Yaw) > 1e-9 {
		t.Fatalf("朝 +X 的目标应让玩家朝向 0，实际 %v", out.Yaw)
	}

	for i := 0; i < 3000; i++ {
		out = arenaStep(arenaStepIn{Dt: 1.0 / 60, Chase: true, Speed: 1.1, Reach: 1.5})
	}
	d := math.Hypot(out.TargetX[0]-out.PlayerX, out.TargetZ[0]-out.PlayerZ)
	if d < 1.2 || d > 2.4 {
		t.Fatalf("玩家应停在追击距离附近，实际距离 %v", d)
	}
	if math.Abs(out.TargetX[0]) > 8+1e-9 || math.Abs(out.TargetZ[0]) > 8+1e-9 {
		t.Fatalf("目标不应跑出场地: (%v, %v)", out.TargetX[0], out.TargetZ[0])
	}
	if math.Abs(out.PlayerX) > 8+1e-9 || math.Abs(out.PlayerZ) > 8+1e-9 {
		t.Fatalf("玩家不应跑出场地: (%v, %v)", out.PlayerX, out.PlayerZ)
	}
}

// TestArenaVerticalSwing：竖直劈砍只打正前方那一层；背后、侧面、太远、太近都不该命中。
func TestArenaVerticalSwing(t *testing.T) {
	if !arenaSetup(arenaSetupIn{
		Player: arenaBody{X: 0, Z: 0, Dir: 0}, // 朝 +X
		Targets: []arenaBody{
			{X: 2.2, Z: 0, Dir: 0},   // 0 正前方 → 命中
			{X: 2.2, Z: 2.0, Dir: 0}, // 1 偏出厚度 → 不中
			{X: -2.2, Z: 0, Dir: 0},  // 2 背后 → 不中
			{X: 7.0, Z: 0, Dir: 0},   // 3 太远 → 不中
			{X: 0.5, Z: 0, Dir: 0},   // 4 贴脸（内圈）→ 不中
		},
		Arena: 10,
	}) {
		t.Fatal("setup 应当成功")
	}
	out := arenaSwing(arenaSwingIn{From: 1.0, To: -0.7, R0: 1.4, R1: 3.0, Thickness: 0.6})
	got := map[int]bool{}
	for _, h := range out.Hits {
		got[h.Index] = true
		if h.Dist <= 0 {
			t.Fatalf("命中距离应为正: %+v", h)
		}
	}
	if !got[0] {
		t.Fatal("正前方的目标应被竖直劈砍命中")
	}
	for _, i := range []int{1, 2, 3, 4} {
		if got[i] {
			t.Fatalf("目标 %d 不该命中", i)
		}
	}
	if out.Candidates < 1 || out.Candidates > 5 {
		t.Fatalf("候选数应在 1..5 之间（宽阶段剔除），实际 %d", out.Candidates)
	}
}

// TestArenaSwingFollowsFacing：劈砍方向跟着玩家朝向走——把目标挪到侧后方，
// 玩家的朝向随之改变，同一个弧度区间就能打到它。
func TestArenaSwingFollowsFacing(t *testing.T) {
	arenaSetup(arenaSetupIn{
		Player:  arenaBody{X: 0, Z: 0, Dir: 0},
		Targets: []arenaBody{{X: 0, Z: 2.4, Dir: 0}},
		Arena:   10,
	})
	// 先转身面向目标（step 会更新 yaw）
	out := arenaStep(arenaStepIn{Dt: 1.0 / 60, Chase: false})
	if math.Abs(out.Yaw-math.Pi/2) > 0.05 { // 目标会同时漫游，朝向有微小偏差
		t.Fatalf("目标在 +Z 方向，朝向应为 π/2，实际 %v", out.Yaw)
	}
	swing := arenaSwing(arenaSwingIn{From: 1.0, To: -0.7, R0: 0.5, R1: 3.0, Thickness: 0.6})
	if len(swing.Hits) != 1 || swing.Hits[0].Index != 0 {
		t.Fatalf("转身后应当命中 +Z 方向的目标，实际 %+v", swing.Hits)
	}
}
