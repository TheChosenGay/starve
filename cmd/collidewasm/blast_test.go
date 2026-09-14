package main

import (
	"math"
	"testing"

	"starve/pkg/collide"
)

// blastScene 建一个固定场景：投掷手在 (-8, -8)，目标按 dirs 摆开。
func blastScene(dirs []blastBody) {
	blastSetup(blastSetupIn{
		Thrower: blastBody{X: -8, Z: -8},
		Targets: dirs,
		Arena:   12,
	})
}

// blastBoomAt 从正上方丢一颗炸弹到 (x, z) 并推进到爆炸，返回这次爆炸的结果。
func blastBoomAt(t *testing.T, x, z, radius, power float64) blastBoomOut {
	t.Helper()
	ray := blastThrowIn{
		Origin: vec3j{X: x, Y: 30, Z: z},
		Dir:    vec3j{Y: -1},
		Max:    100,
		Radius: radius,
		Fuse:   0.05,
		Power:  power,
	}
	if out := blastThrow(ray); !out.Ok {
		t.Fatal("投掷应当落到地面上")
	}
	for i := 0; i < 4; i++ {
		if booms := blastStep(blastStepIn{Dt: 0.02, Speed: 0.11, Damping: 3}).Booms; len(booms) > 0 {
			if len(booms) != 1 {
				t.Fatalf("一次投掷应当只爆一次，实际 %d", len(booms))
			}
			return booms[0]
		}
	}
	t.Fatal("引信走完仍未爆炸")
	return blastBoomOut{}
}

// TestBlastHitsEverythingInsideRadius：爆炸半径内的胶囊全部命中，半径外的都不命中。
//
// 判定口径：爆心（离地 0.35）到胶囊表面的距离 < 半径。目标半径 0.35 时，
// 水平距离 d 的目标表面距离 = d - 0.35，所以 d = 4.3 还中、4.4 就差一点。
func TestBlastHitsEverythingInsideRadius(t *testing.T) {
	blastScene([]blastBody{
		{X: 0, Z: 0, Dir: 0, Speed: 1},   // 0 正踩在爆心上 → 必中，强度 1
		{X: 2, Z: 0, Dir: 0, Speed: 1},   // 1 半径内
		{X: 4.3, Z: 0, Dir: 0, Speed: 1}, // 2 擦边（表面距离 3.95 < 4）→ 中
		{X: 4.4, Z: 0, Dir: 0, Speed: 1}, // 3 刚出界（4.05 > 4）→ 不中
		{X: 12, Z: 0, Dir: 0, Speed: 1},  // 4 太远
	})
	boom := blastBoomAt(t, 0, 0, 4, 7)
	got := map[int]blastHitOut{}
	for _, h := range boom.Hits {
		if _, dup := got[h.Index]; dup {
			t.Fatalf("目标 %d 重复命中", h.Index)
		}
		got[h.Index] = h
	}
	for _, i := range []int{0, 1, 2} {
		if _, ok := got[i]; !ok {
			t.Fatalf("半径内的目标 %d 应当命中，实际命中 %+v", i, boom.Hits)
		}
	}
	for _, i := range []int{3, 4} {
		if _, ok := got[i]; ok {
			t.Fatalf("半径外的目标 %d 不该命中", i)
		}
	}
	if boom.Candidates < len(boom.Hits) {
		t.Fatalf("宽阶段候选 %d 不该少于命中 %d", boom.Candidates, len(boom.Hits))
	}
	if boom.Candidates > 5 {
		t.Fatalf("候选不该超过目标总数，实际 %d", boom.Candidates)
	}
}

// TestBlastIntensityFallsOffWithDistance：闪红/击飞强度就是碰撞查询给的
// 穿透深度 ÷ 半径：贴脸炸（穿透 ≥ 一个半径）= 1，越远越小，擦边 ≈ 0。
func TestBlastIntensityFallsOffWithDistance(t *testing.T) {
	blastScene([]blastBody{
		{X: 0, Z: 0, Dir: 0, Speed: 1},
		{X: 1.5, Z: 0, Dir: 0, Speed: 1},
		{X: 3.0, Z: 0, Dir: 0, Speed: 1},
		{X: 4.3, Z: 0, Dir: 0, Speed: 1},
	})
	boom := blastBoomAt(t, 0, 0, 4, 0) // power 0：只看强度，不看水平击飞
	inten := map[int]float64{}
	depth := map[int]float64{}
	for _, h := range boom.Hits {
		inten[h.Index], depth[h.Index] = h.Intensity, h.Depth
	}
	if math.Abs(inten[0]-1) > 1e-9 {
		t.Fatalf("踩在爆心上应当满强度，实际 %v", inten[0])
	}
	if depth[0] < 4 {
		t.Fatalf("爆心压在目标上时穿透深度应当 ≥ 一个半径（4），实际 %v", depth[0])
	}
	if !(inten[0] > inten[1] && inten[1] > inten[2] && inten[2] > inten[3]) {
		t.Fatalf("强度应当随距离单调下降，实际 %+v", inten)
	}
	if math.Abs(inten[1]-(1-1.15/4)) > 0.03 {
		t.Fatalf("1.5 米处表面距离 1.15，强度应当是 1-1.15/4，实际 %v", inten[1])
	}
	if inten[3] <= 0 || inten[3] > 0.05 {
		t.Fatalf("擦边目标的强度应接近 0，实际 %v", inten[3])
	}
	// 强度 = min(穿透深度 / 半径, 1)：与"1 - 表面距离 / 半径"等价，但直接取自接触信息
	for i, h := range boom.Hits {
		want := math.Min(h.Depth/4, 1)
		if math.Abs(h.Intensity-want) > 1e-9 {
			t.Fatalf("第 %d 个命中强度 %v，按穿透深度应当 %v", i, h.Intensity, want)
		}
	}
}

// TestBlastKnockbackPushesAwayFromCenter：击飞方向 = 背离爆心（左右对称），
// 命中后离地起飞、空中翻滚，最后落回地面；正好压在爆心上的目标没有可推方向，
// 退化成顺着投掷方向炸飞。
func TestBlastKnockbackPushesAwayFromCenter(t *testing.T) {
	blastScene([]blastBody{
		{X: 0, Z: 0, Dir: 0, Speed: 1},
		{X: 2, Z: 0, Dir: 0, Speed: 1},
		{X: -2, Z: 0, Dir: math.Pi, Speed: 1}, // 漫游方向也对称，才比得出"击飞对称"
	})
	boom := blastBoomAt(t, 0, 0, 4, 7)
	normal := map[int]vec3j{}
	hit := map[int]blastHitOut{}
	for _, h := range boom.Hits {
		normal[h.Index] = h.Normal
		hit[h.Index] = h
	}
	if normal[1].X <= 0.99 || math.Abs(normal[1].Z) > 1e-6 {
		t.Fatalf("爆心右侧的目标应当被推向 +X，实际 %+v", normal[1])
	}
	if normal[2].X >= -0.99 {
		t.Fatalf("爆心左侧的目标应当被推向 -X，实际 %+v", normal[2])
	}
	// 踩在爆心上的目标：背离爆心的方向不确定，兜底用投掷方向（投掷手在 (-8,-8)，炸弹在 (0,0)）
	if n := normal[0]; math.Abs(n.X-1/math.Sqrt2) > 0.01 || math.Abs(n.Z-1/math.Sqrt2) > 0.01 {
		t.Fatalf("踩在爆心上的目标应当被顺着投掷方向炸飞，实际 %+v", n)
	}
	if hit[1].Lift <= 0 || hit[1].Speed <= 0 {
		t.Fatalf("命中应当给出向上起飞的速度，实际 %+v", hit[1])
	}
	if blastWorld.agents[1].vy <= 0 {
		t.Fatalf("炸完竖直速度应当向上，实际 %v", blastWorld.agents[1].vy)
	}
	if !(hit[1].Depth > 0 && hit[1].Intensity > 0) {
		t.Fatalf("命中应当带上碰撞查询给的穿透深度与强度，实际 %+v", hit[1])
	}

	// 飞行过程：离地、翻滚，最后落回地面
	peak, spinSeen := 0.0, 0.0
	for i := 0; i < 90; i++ {
		out := blastStep(blastStepIn{Dt: 0.02, Speed: 0.11, Damping: 3})
		peak = math.Max(peak, out.Y[1])
		spinSeen = math.Max(spinSeen, math.Abs(out.Spin[1]))
	}
	if peak < 0.5 {
		t.Fatalf("被炸的目标应当离地飞起来，实际最高只到 %v", peak)
	}
	if spinSeen < 0.5 {
		t.Fatalf("被炸的目标应当在空中翻滚，实际最大翻滚角 %v", spinSeen)
	}
	right := blastWorld.agents[1]
	left := blastWorld.agents[2]
	center := blastWorld.agents[0]
	if right.y != 0 || left.y != 0 {
		t.Fatalf("最终应当落回地面，实际 %v / %v", right.y, left.y)
	}
	if right.x < 2.6 {
		t.Fatalf("右侧目标应当被炸飞得明显更远，实际 x=%v", right.x)
	}
	if left.x > -2.6 {
		t.Fatalf("左侧目标应当被炸飞得明显更远，实际 x=%v", left.x)
	}
	if math.Abs(right.x+left.x) > 0.05 {
		t.Fatalf("左右目标对称，实际 %v / %v", right.x, left.x)
	}
	if center.x < 0.2 || center.z < 0.2 {
		t.Fatalf("爆心上的目标应当被顺着投掷方向炸飞，实际 (%v, %v)", center.x, center.z)
	}
}

// TestBlastLaunchStrengthFollowsDepth：离爆心越近（穿透越深）飞得越高越远——
// 力度不是写死的，直接由碰撞查询给出的穿透深度决定。
func TestBlastLaunchStrengthFollowsDepth(t *testing.T) {
	blastScene([]blastBody{
		{X: 1, Z: 0, Dir: 0, Speed: 1},
		{X: 3.8, Z: 0, Dir: 0, Speed: 1},
	})
	boom := blastBoomAt(t, 0, 0, 4, 7)
	if len(boom.Hits) != 2 {
		t.Fatalf("两个目标都应当在半径内，实际 %+v", boom.Hits)
	}
	near, far := boom.Hits[0], boom.Hits[1]
	if !(near.Depth > far.Depth && near.Intensity > far.Intensity) {
		t.Fatalf("近处目标穿透应当更深、强度更高，实际 %+v / %+v", near, far)
	}
	if !(near.Speed > far.Speed && near.Lift > far.Lift) {
		t.Fatalf("近处目标起飞速度应当更快，实际 %+v / %+v", near, far)
	}
	if blastWorld.agents[0].vx <= blastWorld.agents[1].vx {
		t.Fatalf("近处目标水平击飞速度应当更大，实际 %v / %v",
			blastWorld.agents[0].vx, blastWorld.agents[1].vx)
	}
	nearPeak, farPeak := 0.0, 0.0
	for i := 0; i < 90; i++ {
		out := blastStep(blastStepIn{Dt: 0.02, Speed: 0.11, Damping: 3})
		nearPeak = math.Max(nearPeak, out.Y[0])
		farPeak = math.Max(farPeak, out.Y[1])
	}
	if nearPeak <= farPeak {
		t.Fatalf("近处目标应当飞得更高，实际 %v / %v", nearPeak, farPeak)
	}
	if x := blastWorld.agents[0].x; x < 1.4 {
		t.Fatalf("近处目标应当被炸飞得明显更远，实际 x=%v", x)
	}
}

// TestBlastAirborneTargetTakesLessBlast：飞在空中的目标，同样的水平距离吃到的
// 穿透更浅——高度真的进了碰撞查询，而不是只画在屏幕上。
func TestBlastAirborneTargetTakesLessBlast(t *testing.T) {
	blastScene([]blastBody{
		{X: 1, Z: 0, Dir: 0, Speed: 1}, // 站在地上
		{X: 1, Z: 0, Dir: 0, Speed: 1}, // 同一个水平位置，但悬在 3 米高
	})
	blastWorld.agents[1].y = 3
	blastWorld.agents[1].vy = 1
	blastWorld.engine.Update(blastWorld.handles[1],
		blastCapsule(1, 0, 3, blastTargetRadius, blastTargetHeight))

	boom := blastBoomAt(t, 0, 0, 4, 7)
	inten := map[int]float64{}
	depth := map[int]float64{}
	for _, h := range boom.Hits {
		inten[h.Index], depth[h.Index] = h.Intensity, h.Depth
	}
	if _, ok := inten[0]; !ok {
		t.Fatal("地上的目标应当被炸到")
	}
	if _, ok := inten[1]; !ok {
		t.Fatal("空中的目标仍在爆炸球范围内，只是吃得更浅")
	}
	if !(depth[1] < depth[0] && inten[1] < inten[0]) {
		t.Fatalf("空中目标吃到的穿透应当更浅，实际 地面=%v 空中=%v", depth[0], depth[1])
	}
	if cap, ok := blastWorld.engine.Shape(blastWorld.handles[1]).(collide.Capsule); !ok || cap.A.Y < 2 {
		t.Fatalf("空中目标的胶囊图元底端应当在 2 米以上，实际 %+v",
			blastWorld.engine.Shape(blastWorld.handles[1]))
	}
}

// TestBlastFlashDecays：闪红随时间衰减回 0，且永远不超过 1（同一点连炸两颗也不叠加）。
func TestBlastFlashDecays(t *testing.T) {
	blastScene([]blastBody{{X: 0.5, Z: 0, Dir: 0, Speed: 1}})
	blastBoomAt(t, 0, 0, 4, 7)
	if got := blastWorld.agents[0].flash; got <= 0.5 {
		t.Fatalf("刚炸完应当明显闪红，实际 %v", got)
	}
	// 再炸一次同一个位置：取 max，不叠加
	blastBoomAt(t, 0, 0, 4, 7)
	if got := blastWorld.agents[0].flash; got > 1 {
		t.Fatalf("闪红强度必须 ≤ 1，实际 %v", got)
	}
	for i := 0; i < 60; i++ {
		out := blastStep(blastStepIn{Dt: 0.02, Speed: 0.11, Damping: 3})
		for _, f := range out.Flash {
			if f < 0 || f > 1 {
				t.Fatalf("闪红强度必须在 0..1，实际 %v", f)
			}
		}
	}
	if got := blastWorld.agents[0].flash; got != 0 {
		t.Fatalf("闪红应当衰减回 0，实际 %v", got)
	}
}

// TestBlastFuseDelaysExplosion：引信没走完不爆炸，走完才炸。
func TestBlastFuseDelaysExplosion(t *testing.T) {
	blastScene([]blastBody{{X: 0, Z: 0, Dir: 0, Speed: 1}})
	out := blastThrow(blastThrowIn{
		Origin: vec3j{X: 0, Y: 30, Z: 0},
		Dir:    vec3j{Y: -1},
		Max:    100,
		Radius: 4,
		Fuse:   0.5,
	})
	if !out.Ok {
		t.Fatal("投掷应当成功")
	}
	for i := 0; i < 5; i++ { // 0.1 秒
		step := blastStep(blastStepIn{Dt: 0.02})
		if len(step.Booms) != 0 {
			t.Fatalf("第 %d 帧就爆了，引信还没走完", i)
		}
		if len(step.Bombs) != 1 {
			t.Fatalf("飞行中的炸弹应当还在，实际 %d 颗", len(step.Bombs))
		}
		if step.Bombs[0].Fuse >= 0.5 || step.Bombs[0].Fuse <= 0 {
			t.Fatalf("剩余引信应当在 (0, 0.5) 之间，实际 %v", step.Bombs[0].Fuse)
		}
	}
	var boom blastStepOut
	for i := 0; i < 30 && len(boom.Booms) == 0; i++ {
		boom = blastStep(blastStepIn{Dt: 0.02})
	}
	if len(boom.Booms) != 1 {
		t.Fatal("引信走完应当爆炸")
	}
	if len(boom.Bombs) != 0 {
		t.Fatalf("炸完不该还留着炸弹，实际 %d 颗", len(boom.Bombs))
	}
	if boom.Explosions != 1 || boom.HitTotal != 1 {
		t.Fatalf("累计爆炸 1 次、命中 1 人次，实际 %d / %d", boom.Explosions, boom.HitTotal)
	}
}

// TestBlastAimLandsOnGround：鼠标射线打到地面才算瞄准成功；
// 落点夹在场地内，抬头打不到地面时如实返回 false。
func TestBlastAimLandsOnGround(t *testing.T) {
	blastScene([]blastBody{
		{X: 0, Z: 0, Dir: 0, Speed: 1},
		{X: 2, Z: 0, Dir: 0, Speed: 1},
		{X: 4.3, Z: 0, Dir: 0, Speed: 1},
	})
	aim := blastAim(blastRayIn{
		Origin: vec3j{X: 2, Y: 30, Z: -1},
		Dir:    vec3j{Y: -1},
		Max:    200,
		Radius: 4,
	})
	if !aim.Ok {
		t.Fatal("朝下的射线应当打到地面")
	}
	if math.Abs(aim.X-2) > 1e-9 || math.Abs(aim.Z+1) > 1e-9 || math.Abs(aim.Y) > 1e-9 {
		t.Fatalf("落点应当是 (2, 0, -1)，实际 (%v, %v, %v)", aim.X, aim.Y, aim.Z)
	}
	if aim.Candidates < 3 || aim.Candidates > 3 {
		t.Fatalf("半径 4 应当把三个目标都圈进候选，实际 %d", aim.Candidates)
	}

	// 斜射：从 (0, 10, 0) 朝 +X 斜下方，落点 = 10 米之外
	aim = blastAim(blastRayIn{
		Origin: vec3j{X: 0, Y: 10, Z: 0},
		Dir:    vec3j{X: 1, Y: -1},
		Max:    100,
		Radius: 1,
	})
	if !aim.Ok || math.Abs(aim.X-10) > 1e-6 || math.Abs(aim.Z) > 1e-6 {
		t.Fatalf("45° 斜射 10 米高应当落在 x=10，实际 (%v, %v) ok=%v", aim.X, aim.Z, aim.Ok)
	}

	// 场地外 → 夹到边缘（arena = 12）
	aim = blastAim(blastRayIn{Origin: vec3j{X: 500, Y: 30, Z: 0}, Dir: vec3j{Y: -1}, Max: 200})
	if !aim.Ok || math.Abs(aim.X-12) > 1e-9 {
		t.Fatalf("场地外的落点应当夹到边缘，实际 x=%v", aim.X)
	}

	// 朝天 → 打不到地面
	if aim = blastAim(blastRayIn{Origin: vec3j{X: 0, Y: 30, Z: 0}, Dir: vec3j{Y: 1}, Max: 100}); aim.Ok {
		t.Fatal("朝天的射线不该打中地面")
	}
	// 零向量方向也不该崩
	if aim = blastAim(blastRayIn{Origin: vec3j{X: 0, Y: 30, Z: 0}, Dir: vec3j{}, Max: 100}); aim.Ok {
		t.Fatal("零方向不该打中地面")
	}
}
