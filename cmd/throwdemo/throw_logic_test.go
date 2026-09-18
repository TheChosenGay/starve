package main

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 演示逻辑的契约测试（宿主机跑，不需要 WASM）。
//
// 价值：如果投掷机制被改坏，throw.html 会显示"扔不出去/不爆炸"，
// 但那是浏览器里肉眼不易定位的静默失败。这里先把行为钉住。

func TestDemoWorldBuilds(t *testing.T) {
	w := newThrowWorld(8, 10)
	snap := w.snapshot()
	if len(snap.Props) != 8 {
		t.Fatalf("应有 8 件投掷物，实际 %d", len(snap.Props))
	}
	if len(snap.Beasts) != 10 {
		t.Fatalf("应有 10 只野兽，实际 %d", len(snap.Beasts))
	}
	if snap.Player.Strength != demoStrength {
		t.Fatalf("投掷者力量应为 %d，实际 %d", demoStrength, snap.Player.Strength)
	}
	// 切片不能为 nil（会 JSON 成 null 导致前端白屏）
	if snap.Props == nil || snap.Beasts == nil || snap.Events == nil {
		t.Fatal("快照切片不能为 nil")
	}
}

// 距离上限必须随质量变化：越重扔越近（演示的核心观感）。
func TestHeavierPropHasShorterMaxDistance(t *testing.T) {
	w := newThrowWorld(8, 4)
	seen := map[int]int{} // mass -> maxDist
	for i := range w.props {
		w.selectProp(i)
		snap := w.snapshot()
		mass := snap.Props[i].Mass
		seen[mass] = snap.Player.MaxDist
	}
	t.Logf("质量 → 最大距离: %v", seen)
	if len(seen) < 2 {
		t.Fatal("演示应包含不同质量的投掷物")
	}
	// 质量越大，距离必须越小或相等（单调不增）
	for m1, d1 := range seen {
		for m2, d2 := range seen {
			if m2 > m1 && d2 > d1 {
				t.Fatalf("质量 %d 的距离 %d 不应大于更轻的 %d（距离 %d）", m2, d2, m1, d1)
			}
		}
	}
}

// 力量滑块必须立刻影响可达距离。
func TestStrengthChangesMaxDistance(t *testing.T) {
	w := newThrowWorld(4, 4)
	w.selectProp(0)
	w.setStrength(10)
	d1 := w.snapshot().Player.MaxDist
	w.setStrength(30)
	d2 := w.snapshot().Player.MaxDist
	if d2 <= d1 {
		t.Fatalf("力量从 10 提到 30，最大距离应增大：%d -> %d", d1, d2)
	}
	// 力量 0 = 不能投掷
	w.setStrength(0)
	if d := w.snapshot().Player.MaxDist; d != 0 {
		t.Fatalf("力量为 0 时距离应为 0，实际 %d", d)
	}
}

// 瞄准后应给出预览抛物线（含飞行时长与峰值高度）。
func TestAimProducesPreviewArc(t *testing.T) {
	w := newThrowWorld(4, 4)
	w.selectProp(0)
	// 瞄准投掷物右侧几格（必在范围内）
	prop := w.props[0]
	p := ecs.Get[components.Position](w.sim, prop)
	w.aimAt(float64(p.X)+3, float64(p.Y))

	snap := w.snapshot()
	if !snap.CanThrow {
		t.Fatalf("近距离瞄准应可投掷，被拒原因：%s", snap.BlockReason)
	}
	if snap.PreviewArc == nil {
		t.Fatal("可投掷时应给出预览抛物线")
	}
	if snap.PreviewArc.FlightTicks < 1 {
		t.Fatalf("飞行时长应 >= 1，实际 %d", snap.PreviewArc.FlightTicks)
	}
	if snap.PreviewArc.PeakHeight <= 0 {
		t.Fatalf("峰值高度应为正，实际 %.3f", snap.PreviewArc.PeakHeight)
	}
}

// 超出最大距离必须被拒（并且给出**可读的原因**，而不是静默失败）。
func TestTooFarAimIsRejectedWithReason(t *testing.T) {
	w := newThrowWorld(4, 4)
	w.selectProp(2) // 选重的（巨石），距离上限小
	prop := w.props[2]
	p := ecs.Get[components.Position](w.sim, prop)
	w.aimAt(float64(p.X)+60, float64(p.Y)) // 远超上限

	snap := w.snapshot()
	if snap.CanThrow {
		t.Fatal("超距离的瞄准必须被拒")
	}
	if snap.BlockReason == "" {
		t.Fatal("被拒时必须给出原因（前端要显示'为什么不能扔'）")
	}
	t.Logf("超距离被拒原因：%s", snap.BlockReason)
	if snap.PreviewArc != nil {
		t.Fatal("被拒时不应给出预览抛物线")
	}
}

// 完整投掷流程：投出 → 飞行若干 tick → 落地爆炸。
func TestThrowFliesAndExplodes(t *testing.T) {
	w := newThrowWorld(4, 4)
	w.selectProp(0)
	prop := w.props[0]
	// **拷贝**位置值，不能持有指针：飞行过程中 ThrowSystem 会原地修改
	// Position 组件，指针会让"起点/瞄准点"随飞行一起漂移（实测踩过，
	// 断言里 p.X 从 29 变成 33，看起来像落点算错，实际是测试的别名 bug）。
	startX := ecs.Get[components.Position](w.sim, prop).X
	startY := ecs.Get[components.Position](w.sim, prop).Y
	aimX := startX + 4
	w.aimAt(float64(aimX), float64(startY))

	if !w.doThrow() {
		t.Fatal("近距离投掷应成功")
	}
	// 投出后应处于飞行状态
	if !ecs.Has[components.Thrown](w.sim, prop) {
		t.Fatal("投出后被投物应处于飞行状态（挂 Thrown）")
	}
	flying := w.snapshot()
	if !flying.Props[0].Flying {
		t.Fatal("快照应报告飞行中")
	}
	t.Logf("飞行 %d tick", flying.Props[0].FlightTicks)

	// 推进到落地
	flight := flying.Props[0].FlightTicks
	for i := 0; i < flight+2; i++ {
		w.step()
	}
	if ecs.Has[components.Thrown](w.sim, prop) {
		t.Fatal("飞行结束后应移除 Thrown（落地）")
	}
	// 应产生爆炸表现
	if len(w.blasts) == 0 {
		t.Fatal("落地应产生爆炸（前端要画扩散圈）")
	}
	// 投掷物应精确落在瞄准点（水平匀速 ⇒ 最后一 tick 正好命中）。
	//
	// ⚠️ 爆炸物落地即被**消耗**（实体被销毁，见 throw_system.go land()：否则地上会残留
	// 一颗可拾取的炸弹 = 无限炸弹），所以落点不能再从投掷物实体读 —— 从**爆炸事件**读，
	// 那本来就是权威落点（EmitBlast 用的就是 th.ToX/ToY）。
	if w.sim.IsAlive(prop) {
		t.Fatal("爆炸物落地应被消耗（销毁实体）：地上不该残留可拾取炸弹")
	}
	last := w.blasts[len(w.blasts)-1]
	if last.x != float64(aimX) {
		t.Fatalf("爆炸中心 X 应精确等于瞄准点 %d，实际 %v", aimX, last.x)
	}
}

// 投掷物落在野兽附近时，被炸的生物应**记仇**（与群体仇恨联动）。
//
// 这是本演示最有价值的一条：验证爆炸伤害确实走了 ApplyDamage
// （而不是直接扣血），否则"炸了一片怪，没一个理你"。
func TestBlastMakesBeastsAggroThrower(t *testing.T) {
	w := newThrowWorld(4, 10)

	// 找一只野兽，把投掷物挪到它旁边（保证在爆炸半径内）
	beast := w.beasts[0]
	bp := ecs.Get[components.Position](w.sim, beast)
	prop := w.props[0]
	// 把投掷物放到野兽旁边 2 格（仍需在投掷者"手里"范围外，
	// 所以先把投掷者也挪过去，保证校验通过）
	pp := ecs.Get[components.Position](w.sim, w.player)
	pp.X, pp.Y = bp.X-2, bp.Y
	ecs.MarkDirty[components.Position](w.sim, w.player)
	ecs.Get[components.Position](w.sim, prop).X = bp.X - 1
	ecs.Get[components.Position](w.sim, prop).Y = bp.Y
	ecs.MarkDirty[components.Position](w.sim, prop)

	w.selectProp(0)
	w.aimAt(float64(bp.X), float64(bp.Y))
	if !w.snapshot().CanThrow {
		t.Fatalf("应可投掷，被拒：%s", w.snapshot().BlockReason)
	}
	if !w.doThrow() {
		t.Fatal("投掷应成功")
	}
	for i := 0; i < 40; i++ {
		w.step()
	}

	hp := ecs.Get[components.Health](w.sim, beast)
	if hp.Cur >= hp.Max {
		t.Fatalf("爆炸应伤到野兽（HP %d/%d 未变）", hp.Cur, hp.Max)
	}
	// 关键：必须记仇（走 ApplyDamage 才会产生直接仇恨）
	if !ecs.Get[components.Creature](w.sim, beast).IsDirectThreat(w.player) {
		t.Fatal("被炸的野兽应对投掷者记仇（说明爆炸伤害没走 ApplyDamage）")
	}
	t.Logf("野兽 HP %d/%d，已对投掷者记仇", hp.Cur, hp.Max)
}

// 瞄准落点不可站立时应被拒（不扔进地图外）。
func TestAimOutsideFieldIsRejected(t *testing.T) {
	w := newThrowWorld(4, 4)
	w.selectProp(0)
	w.aimAt(-50, -50)
	snap := w.snapshot()
	if snap.CanThrow {
		t.Fatal("地图外的落点必须被拒")
	}
	if snap.BlockReason == "" {
		t.Fatal("应给出拒绝原因")
	}
}

// reset 应恢复初始状态。
func TestResetRestoresState(t *testing.T) {
	w := newThrowWorld(4, 4)
	w.selectProp(0)
	// 同样要拷贝：不要持有 Position 指针
	px := ecs.Get[components.Position](w.sim, w.props[0]).X
	py := ecs.Get[components.Position](w.sim, w.props[0]).Y
	w.aimAt(float64(px)+3, float64(py))
	if !w.doThrow() {
		t.Fatal("前置条件：投掷应成功")
	}
	for i := 0; i < 30; i++ {
		w.step()
	}
	// 用**累计计数**判断，而不是 len(blasts)：后者是表现，0.5 秒后会过期，
	// 跑满 30 tick 时早已移除，用它断言会误判"没爆炸"（实测踩过）。
	if w.blastCount == 0 {
		t.Fatal("前置条件：应已产生爆炸")
	}

	w.reset(6, 5)
	snap := w.snapshot()
	if len(snap.Props) != 6 || len(snap.Beasts) != 5 {
		t.Fatalf("重置后应为 6 投掷物 / 5 野兽，实际 %d / %d", len(snap.Props), len(snap.Beasts))
	}
	if snap.HasAim {
		t.Fatal("重置后不应有瞄准点")
	}
	if len(w.blasts) != 0 {
		t.Fatalf("重置后不应残留爆炸表现，实际 %d", len(w.blasts))
	}
	if snap.BlastCount != 0 {
		t.Fatalf("重置后累计爆炸次数应清零，实际 %d", snap.BlastCount)
	}
}
