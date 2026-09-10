package collide

import "testing"

// 场景 1：近战攻击命中判定（角色用胶囊表示）。
func TestAttackMeleeCapsule(t *testing.T) {
	attacker := Capsule{A: Vec3{X: 0, Y: 0, Z: 0}, B: Vec3{X: 0, Y: 1.8, Z: 0}, R: 0.35}

	// 目标轴距 0.8 > 半径和 0.75 => 未命中
	far := Capsule{A: Vec3{X: 0.8, Y: 0, Z: 0}, B: Vec3{X: 0.8, Y: 1.8, Z: 0}, R: 0.4}
	if TestCapsuleCapsule(attacker, far) {
		t.Fatal("0.8 axis distance should be out of reach (sum radii 0.75)")
	}

	// 目标轴距 0.7 <= 0.75 => 命中
	near := Capsule{A: Vec3{X: 0.7, Y: 0, Z: 0}, B: Vec3{X: 0.7, Y: 1.8, Z: 0}, R: 0.4}
	if !TestCapsuleCapsule(attacker, near) {
		t.Fatal("0.7 axis distance should be a hit")
	}

	// 高度错开：目标整体抬高到轴距不变但垂直不重叠也不该命中
	high := Capsule{A: Vec3{X: 0, Y: 3.0, Z: 0}, B: Vec3{X: 0, Y: 4.8, Z: 0}, R: 0.4}
	if TestCapsuleCapsule(attacker, high) {
		t.Fatal("vertically separated capsules should not hit")
	}
}

// 场景 2：子弹射击——是否命中、何时命中（距离/时间）、命中在哪里。
func TestAttackBulletHitscan(t *testing.T) {
	muzzle := Vec3{X: 0, Y: 1.5, Z: 0}
	dir := Vec3{X: 1, Y: 0, Z: 0}
	target := Sphere{C: Vec3{X: 12, Y: 1.5, Z: 0}, R: 0.6}
	const speed = 60.0 // 米/秒

	h := IntersectRaySphere(muzzle, dir, speed, target) // 1 秒的射程
	if !h.Hit {
		t.Fatal("bullet should hit target")
	}
	if !almostEq(h.Dist, 11.4) { // 12 - 0.6
		t.Fatalf("hit distance = %v, want 11.4", h.Dist)
	}
	if !almostEq(h.Dist/speed, 0.19) { // 何时命中
		t.Fatalf("time to hit = %v, want 0.19s", h.Dist/speed)
	}
	if !vecAlmostEq(h.Point, Vec3{X: 11.4, Y: 1.5, Z: 0}) { // 命中在哪
		t.Fatalf("hit point = %v, want (11.4,1.5,0)", h.Point)
	}

	// 目标横向移开，且射程只够 1 秒 -> 未命中
	miss := Sphere{C: Vec3{X: 12, Y: 1.5, Z: 3}, R: 0.6}
	if h := IntersectRaySphere(muzzle, dir, speed, miss); h.Hit {
		t.Fatal("bullet should miss the laterally offset target")
	}

	// 目标在射程之外 -> 未命中（有限范围）
	tooFar := Sphere{C: Vec3{X: 120, Y: 1.5, Z: 0}, R: 0.6}
	if h := IntersectRaySphere(muzzle, dir, speed, tooFar); h.Hit {
		t.Fatal("bullet out of range should not hit")
	}
}

// 场景 3：高速弹丸对一排障碍，取第一个撞到的（防隧穿 + 谁被撞）。
func TestAttackBulletSweepFirst(t *testing.T) {
	bullet := Sphere{C: Vec3{X: 0, Y: 1, Z: 0}, R: 0.25}
	motion := Vec3{X: 40, Y: 0, Z: 0} // 一帧飞 40 米
	walls := []AABB{
		{Min: Vec3{X: 5, Y: 0, Z: -2}, Max: Vec3{X: 5.2, Y: 3, Z: 2}},
		{Min: Vec3{X: 9, Y: 0, Z: -2}, Max: Vec3{X: 9.2, Y: 3, Z: 2}},
		{Min: Vec3{X: 20, Y: 0, Z: -2}, Max: Vec3{X: 20.2, Y: 3, Z: 2}},
	}
	idx, h, ok := SweepFirstSphereAABB(bullet, motion, walls)
	if !ok || idx != 0 {
		t.Fatalf("should hit wall 0 first, got idx=%d ok=%v", idx, ok)
	}
	if !almostEq(h.Point.X, 5) { // 命中在最近那面墙上
		t.Fatalf("hit x = %v, want 5", h.Point.X)
	}
	if !almostEq(h.T, 4.75/40) { // 球心到 5-0.25=4.75
		t.Fatalf("T = %v, want %v", h.T, 4.75/40)
	}
}
