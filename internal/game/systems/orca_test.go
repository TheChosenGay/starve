package systems

import (
	"math"
	"testing"
)

// 无障碍时原样返回期望速度。
func TestORCAOpenFieldReturnsPreferred(t *testing.T) {
	s := NewORCASolver(DefaultORCAOptions())
	vx, vy := s.Solve(Agent{VX: 1, VY: 0, PrefVX: 1, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 10}, nil)
	if math.Abs(vx-1) > 1e-9 || math.Abs(vy) > 1e-9 {
		t.Fatalf("空旷处应返回期望速度, got (%.4f,%.4f)", vx, vy)
	}
}

// 正面对撞：双方相向而行，应产生横向分量互相绕开，而不是双双停死。
//
// 注意必须用 NewORCASolverFor（带对称打破）：纯 ORCA 在完全对称的正面对撞下
// 是退化的（约束法向与期望速度共线），最优解是双方都减速停住——经典死锁。
func TestORCAHeadOnAvoids(t *testing.T) {
	s := NewORCASolverFor(DefaultORCAOptions(), 1)
	self := Agent{VX: 1, VY: 0, PrefVX: 1, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 1}
	other := ORCABody{X: 0.8, Z: 0, VX: -1, VY: 0, Radius: 0.3, MaxSpeed: 1}

	vx, vy := s.Solve(self, []ORCABody{other})
	// 关键：横向必须产生偏移（绕开），而不是继续直冲或停死
	if math.Abs(vy) < 1e-6 {
		t.Fatalf("相向而行应产生横向避让分量, got (%.4f,%.4f)", vx, vy)
	}
	if l := math.Hypot(vx, vy); l > 1+1e-6 {
		t.Fatalf("速度超上限: %.4f", l)
	}
}

// 对称打破必须让**双方往相反方向**让（否则两人同向绕、还是撞）。
func TestORCASymmetryBreakSendsSidesOpposite(t *testing.T) {
	opts := DefaultORCAOptions()
	self := Agent{VX: 1, VY: 0, PrefVX: 1, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 1}
	other := ORCABody{X: 0.8, Z: 0, VX: -1, VY: 0, Radius: 0.3, MaxSpeed: 1}

	// 奇数 id 往一侧
	a := NewORCASolverFor(opts, 1)
	_, ay := a.Solve(self, []ORCABody{other})
	// 偶数 id 往另一侧
	b := NewORCASolverFor(opts, 2)
	_, by := b.Solve(self, []ORCABody{other})

	if ay*by >= 0 {
		t.Fatalf("奇偶 id 应向相反两侧避让, got ay=%.5f by=%.5f", ay, by)
	}
}

// 同一对实体反复求解结果稳定（不会这一 tick 左、下一 tick 右地抖）。
func TestORCASymmetryStableAcrossTicks(t *testing.T) {
	opts := DefaultORCAOptions()
	s := NewORCASolverFor(opts, 3)
	self := Agent{VX: 1, VY: 0, PrefVX: 1, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 1}
	other := ORCABody{X: 0.8, Z: 0, VX: -1, VY: 0, Radius: 0.3, MaxSpeed: 1}

	_, y1 := s.Solve(self, []ORCABody{other})
	_, y2 := s.Solve(self, []ORCABody{other})
	if math.Abs(y1-y2) > 1e-12 {
		t.Fatalf("同样输入应给同样输出: %.8f vs %.8f", y1, y2)
	}
}

// 对方在远处且正在远离：不应产生任何避让（否则远处的人会让人无谓绕路）。
func TestORCAIgnoresRecedingFarNeighbor(t *testing.T) {
	s := NewORCASolver(DefaultORCAOptions())
	self := Agent{VX: 1, VY: 0, PrefVX: 1, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 1}
	far := ORCABody{X: 50, Z: 0, VX: 5, VY: 0, Radius: 0.3, MaxSpeed: 5}
	vx, vy := s.Solve(self, []ORCABody{far})
	if math.Abs(vx-1) > 1e-9 || math.Abs(vy) > 1e-9 {
		t.Fatalf("远处正在远离的邻居不该影响速度, got (%.4f,%.4f)", vx, vy)
	}
}

// 对方同向同速并行（不构成威胁）：也不该避让。
func TestORCAParallelSameSpeedNoAvoid(t *testing.T) {
	s := NewORCASolver(DefaultORCAOptions())
	self := Agent{VX: 1, VY: 0, PrefVX: 1, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 1}
	// 正右方 1.5 格，同向同速：距离不变，永不相撞
	other := ORCABody{X: 0, Z: 1.5, VX: 1, VY: 0, Radius: 0.3, MaxSpeed: 1}
	vx, vy := s.Solve(self, []ORCABody{other})
	if math.Abs(vx-1) > 1e-6 || math.Abs(vy) > 1e-6 {
		t.Fatalf("同向同速并行不该避让, got (%.4f,%.4f)", vx, vy)
	}
}

// 已经被挤住/重叠：必须产生"分开"的速度（法向朝外）。
func TestORCASeparatesWhenOverlapping(t *testing.T) {
	s := NewORCASolver(DefaultORCAOptions())
	self := Agent{VX: 0, VY: 0, PrefVX: 0, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 1}
	// 邻居几乎压在身上（距离 0.1 < 半径和 0.6）
	other := ORCABody{X: 0.1, Z: 0, VX: 0, VY: 0, Radius: 0.3, MaxSpeed: 1}
	vx, vy := s.Solve(self, []ORCABody{other})
	if l := math.Hypot(vx, vy); l < 1e-6 {
		t.Fatalf("重叠时应产生分离速度, got (%.4f,%.4f)", vx, vy)
	}
	// 分离方向应背离邻居（+X 侧），即 vx > 0
	if vx <= 0 {
		t.Fatalf("分离速度应朝远离邻居的方向, got (%.4f,%.4f)", vx, vy)
	}
}

// 被多方向围死时不能返回 NaN/Inf（兜底路径）。
func TestORCASurroundedStaysFinite(t *testing.T) {
	s := NewORCASolver(DefaultORCAOptions())
	self := Agent{VX: 1, VY: 0, PrefVX: 1, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 1}
	var ns []ORCABody
	for i := 0; i < 8; i++ {
		ang := 2 * math.Pi * float64(i) / 8
		ns = append(ns, ORCABody{
			X: math.Cos(ang) * 0.5, Z: math.Sin(ang) * 0.5,
			Radius: 0.3, MaxSpeed: 1,
		})
	}
	vx, vy := s.Solve(self, ns)
	if math.IsNaN(vx) || math.IsNaN(vy) || math.IsInf(vx, 0) || math.IsInf(vy, 0) {
		t.Fatalf("被围死时速度必须有限, got (%v,%v)", vx, vy)
	}
	if l := math.Hypot(vx, vy); l > 1+1e-6 {
		t.Fatalf("速度超上限: %.4f", l)
	}
}

// 确定性：同样的输入必须给同样的输出（邻居顺序不同也一样）。
func TestORCADeterministicRegardlessOfOrder(t *testing.T) {
	s := NewORCASolver(DefaultORCAOptions())
	self := Agent{VX: 1, VY: 0, PrefVX: 1, PrefVY: 0, X: 0, Z: 0, Radius: 0.3, MaxSpeed: 1}
	a := ORCABody{X: 0.8, Z: 0.1, VX: -1, VY: 0, Radius: 0.3, MaxSpeed: 1}
	b := ORCABody{X: 0.9, Z: -0.2, VX: -1, VY: 0, Radius: 0.3, MaxSpeed: 1}

	v1x, v1y := s.Solve(self, []ORCABody{a, b})
	v2x, v2y := s.Solve(self, []ORCABody{b, a})
	if math.Abs(v1x-v2x) > 1e-12 || math.Abs(v1y-v2y) > 1e-12 {
		t.Fatalf("邻居顺序不应影响结果: (%.6f,%.6f) vs (%.6f,%.6f)", v1x, v1y, v2x, v2y)
	}
}
