package collide

import (
	"math"
	"testing"
)

// 胶囊正面撞树：前端半球先接触（长宽刚好包住模型的四足就靠它不被鼻子穿模）。
func TestSweepSlideCapsuleFrontContact(t *testing.T) {
	eng := slideEngine(t, trunk(3, 0, 0, 0.25, 3))
	// 段沿 X：中心 → 前端 (centerX+0.5)；接触条件 centerX+0.5+0.2+0.25 = 3
	a := Vec3{X: -0.5, Y: 1}
	b := Vec3{X: 0.5, Y: 1}
	res := eng.SweepSlideCapsule(a, b, 0.2, Vec3{X: 3}, nil, SlideOptions{})
	centerX := 0 + res.Moved.X
	if !res.Blocked {
		t.Fatal("正面撞树应有接触")
	}
	if math.Abs(centerX-2.05) > 2e-3 {
		t.Fatalf("中心应停在 x≈2.05（前端刚好碰到树），实际 %.4f", centerX)
	}
}

// 侧向蹭树：胶囊的"长"会拦住侧面的障碍——这是圆（点）移动体会漏掉的情形。
func TestSweepSlideCapsuleSideContact(t *testing.T) {
	eng := slideEngine(t, trunk(0, 0, 0.9, 0.25, 3))
	// 段沿 X（x ∈ [-1,1]），从 z=-0.5 往 +Z 走：侧面在 z±0.2 → 应在 z≈0.45 处被拦住
	a := Vec3{X: -1, Y: 1, Z: -0.5}
	b := Vec3{X: 1, Y: 1, Z: -0.5}
	res := eng.SweepSlideCapsule(a, b, 0.2, Vec3{Z: 2}, nil, SlideOptions{})
	endZ := -0.5 + res.Moved.Z
	if !res.Blocked {
		t.Fatalf("侧面应撞上树（长条模型的体长侧向也占空间），实际移到 z=%.4f", endZ)
	}
	if math.Abs(endZ-(0.9-0.45)) > 3e-3 {
		t.Fatalf("侧面应停在 z≈0.45，实际 %.4f", endZ)
	}
}

// 细缝不能钻：缝宽小于胶囊直径时，即使"段"（一条线）能过，胶囊也过不去。
func TestSweepSlideCapsuleCannotSqueezeThroughGap(t *testing.T) {
	// 两棵树在 z=±0.3、半径 0.15：自由缝 z ∈ [-0.15, 0.15]（0.3 宽），
	// 胶囊半径 0.2（直径 0.4）侧向占 z±0.2 → 过不去；而"段"在 z=0 是能过的。
	eng := slideEngine(t,
		trunk(0, 0, -0.3, 0.15, 3),
		trunk(0, 0, 0.3, 0.15, 3),
	)
	a := Vec3{X: -2, Y: 1, Z: 0}
	b := Vec3{X: -1, Y: 1, Z: 0}
	res := eng.SweepSlideCapsule(a, b, 0.2, Vec3{X: 3}, nil, SlideOptions{})
	if res.Hits == 0 {
		t.Fatal("应记到接触")
	}
	centerX := -1.5 + res.Moved.X // 参考点（段中点）最终位置
	if centerX > 0.2 {
		t.Fatalf("缝太窄不该钻过去：中心 x=%.3f 已越过障碍", centerX)
	}
	// 两侧障碍对称地夹住胶囊：横向几乎不动，说明它被"捏"在缝口（而非挤过去）
	if math.Abs(res.Moved.Z) > 0.2 {
		t.Fatalf("对称夹缝不该把胶囊推出去，实际 z 位移 %.3f", res.Moved.Z)
	}
}

// 空场快路径：整段扫掠盒里没有候选 → 原样返回请求位移。
func TestSweepSlideCapsuleOpenField(t *testing.T) {
	eng := slideEngine(t, trunk(50, 0, 50, 0.25, 3))
	a := Vec3{X: -0.5, Y: 1}
	b := Vec3{X: 0.5, Y: 1}
	motion := Vec3{X: 0.3, Z: -0.2}
	res := eng.SweepSlideCapsule(a, b, 0.2, motion, nil, SlideOptions{})
	if res.Hits != 0 || res.Moved.Sub(motion).Len() > 1e-12 {
		t.Fatalf("空场应原样位移, got moved=(%.3f,%.3f) hits=%d", res.Moved.X, res.Moved.Z, res.Hits)
	}
}
