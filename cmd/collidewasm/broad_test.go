package main

import (
	"math"
	"testing"
)

// TestBPQueryModesAgree：两个模式命中的目标必须完全一致——
// 扫描器只允许少调窄阶段，不允许改变判定结果。
func TestBPQueryModesAgree(t *testing.T) {
	bpSetup(bpSetupIn{Count: 400, Seed: 3, Bound: 30, Margin: 0.25, Radius: 0.4, Height: 1.8})
	for tick := 0; tick < 6; tick++ {
		bpStep(bpStepIn{Dt: 1.0 / 60, Speed: 6, Dirty: 1})
		naive := bpQuery(bpQueryIn{Mode: "naive", Count: 20, Radius: 1.6})
		scanned := bpQuery(bpQueryIn{Mode: "scanner", Count: 20, Radius: 1.6})
		if naive.Hits != scanned.Hits {
			t.Fatalf("第 %d 帧：naive 命中 %d，scanner 命中 %d", tick, naive.Hits, scanned.Hits)
		}
		if naive.Narrow != 20*400 {
			t.Fatalf("naive 应当每次都扫全部物体，实际 %d", naive.Narrow)
		}
		if scanned.Narrow > naive.Narrow {
			t.Fatalf("扫描器不该比暴力还多调窄阶段：%d > %d", scanned.Narrow, naive.Narrow)
		}
		t.Logf("第 %d 帧：naive 窄阶段 %d 次 / 扫描器 %d 次（命中 %d）",
			tick, naive.Narrow, scanned.Narrow, naive.Hits)
	}
}

// TestBPScannerCulls：扫描器必须真的把候选砍下来（否则宽阶段就没有意义）。
func TestBPScannerCulls(t *testing.T) {
	bpSetup(bpSetupIn{Count: 2000, Seed: 9, Bound: 40, Margin: 0.25, Radius: 0.4, Height: 1.8})
	bpStep(bpStepIn{Dt: 1.0 / 60, Speed: 6, Dirty: 1})
	naive := bpQuery(bpQueryIn{Mode: "naive", Count: 20, Radius: 1.6})
	scanned := bpQuery(bpQueryIn{Mode: "scanner", Count: 20, Radius: 1.6})
	t.Logf("2000 物体 × 20 查询：naive 窄阶段 %d 次（%.2f ms），扫描器候选 %d 次 / 窄阶段 %d 次（%.2f ms）",
		naive.Narrow, naive.Ms, scanned.Candidates, scanned.Narrow, scanned.Ms)
	if scanned.Candidates >= naive.Narrow/5 {
		t.Fatalf("候选数应当远小于全部物体：候选 %d，全部 %d", scanned.Candidates, naive.Narrow)
	}
}

// TestBPStepDirty：只有比例内的物体移动时，索引更新也只处理那一部分。
func TestBPStepDirty(t *testing.T) {
	bpSetup(bpSetupIn{Count: 100, Seed: 5, Bound: 20, Margin: 0.5, Radius: 0.4, Height: 1.8})
	before := append([]vec3j(nil), bpStep(bpStepIn{Dt: 1.0 / 60, Speed: 6, Dirty: 0.1}).Positions...)
	after := bpStep(bpStepIn{Dt: 1.0 / 60, Speed: 6, Dirty: 0.1}).Positions
	moved, stayed := 0, 0
	for i := range before {
		if before[i] != after[i] {
			moved++
		} else {
			stayed++
		}
	}
	if moved == 0 || stayed == 0 {
		t.Fatalf("dirty=0.1 时应当只有一部分物体移动：动了 %d，没动 %d", moved, stayed)
	}
}

// TestSectorStep：扇形场景的命中集合与命中部位。
func TestSectorStep(t *testing.T) {
	setup := sectorSetupIn{
		Player: sectorBody{X: 0, Z: 0, Radius: 0.4, Height: 1.8},
		Targets: []sectorBody{
			{X: 2, Z: 0, Radius: 0.4, Height: 1.8},   // 正前方
			{X: 2, Z: 1.4, Radius: 0.4, Height: 1.8}, // 约 35°，在 45° 内
			{X: 0, Z: 2, Radius: 0.4, Height: 1.8},   // 90°，在外
			{X: -2, Z: 0, Radius: 0.4, Height: 1.8},  // 背后
			{X: 8, Z: 0, Radius: 0.4, Height: 1.8},   // 超出射程
			{X: 0.1, Z: 0, Radius: 0.4, Height: 1.8}, // 贴脸，在内圈里
		},
	}
	if !sectorSetup(setup) {
		t.Fatal("setup 应当成功")
	}
	out := sectorStep(sectorStepIn{From: -math.Pi / 4, To: math.Pi / 4, R0: 0.8, R1: 5})
	got := map[int]bool{}
	for _, h := range out.Hits {
		got[h.Index] = true
		if h.Dist <= 0 {
			t.Fatalf("命中距离应为正: %+v", h)
		}
	}
	for _, i := range []int{0, 1} {
		if !got[i] {
			t.Fatalf("目标 %d 应当命中", i)
		}
	}
	for _, i := range []int{2, 3, 4, 5} {
		if got[i] {
			t.Fatalf("目标 %d 不该命中", i)
		}
	}
	if out.Candidates > len(setup.Targets) {
		t.Fatalf("候选数不该超过代理总数：%d", out.Candidates)
	}
}

// TestSectorSweepProgression：挥砍过程中，越靠近正前方的目标越早被扫到。
func TestSectorSweepProgression(t *testing.T) {
	sectorSetup(sectorSetupIn{
		Player: sectorBody{X: 0, Z: 0, Radius: 0.4, Height: 1.8},
		Targets: []sectorBody{
			{X: 2, Z: -1.2, Radius: 0.3, Height: 1.8}, // 约 -31°
			{X: 2, Z: 1.2, Radius: 0.3, Height: 1.8},  // 约 +31°
		},
	})
	const from = -1.0
	firstAt := map[int]float64{}
	for step := 0; step <= 40; step++ {
		to := from + 2.0*float64(step)/40
		out := sectorStep(sectorStepIn{From: from, To: to, R0: 0.5, R1: 5})
		for _, h := range out.Hits {
			if _, seen := firstAt[h.Index]; !seen {
				firstAt[h.Index] = to
			}
		}
	}
	if len(firstAt) != 2 {
		t.Fatalf("两个目标都应被扫到，实际 %v", firstAt)
	}
	if !(firstAt[0] < firstAt[1]) {
		t.Fatalf("负角度目标应先被扫到：%v", firstAt)
	}
}
