package collision

import (
	"math"
	"testing"

	"starve/internal/ecs"
)

// 注册/更新/注销/重建：句柄与计数跟随实体，重复操作幂等。
func TestIndexSetClearReset(t *testing.T) {
	c := NewIndex()
	if c.Len() != 0 {
		t.Fatalf("空索引 Len=%d", c.Len())
	}
	c.Set(1, 1.5, 1.5, 0.2)
	c.Set(2, 5.5, 5.5, 0.3)
	c.Set(1, 1.5, 1.5, 0.25) // 更新半径：仍是同一个实体，不新增
	if c.Len() != 2 {
		t.Fatalf("更新后 Len=%d, want 2", c.Len())
	}
	if _, ok := c.handles[1]; !ok {
		t.Fatal("应按实体登记句柄")
	}
	if _, ok := c.handles[2]; !ok {
		t.Fatal("应按实体登记句柄")
	}
	c.Clear(2)
	c.Clear(2) // 幂等
	if c.Len() != 1 {
		t.Fatalf("注销后 Len=%d, want 1", c.Len())
	}
	c.Reset()
	if c.Len() != 0 || len(c.handles) != 0 {
		t.Fatalf("Reset 后应清空, len=%d handles=%d", c.Len(), len(c.handles))
	}
}

// 半径 ≤ 0 视为无碰撞体（模板没配 collision_radius 的实体不该挡人）。
func TestIndexZeroRadiusClears(t *testing.T) {
	c := NewIndex()
	c.Set(1, 1.5, 1.5, 0.2)
	c.Set(1, 1.5, 1.5, 0)
	if c.Len() != 0 {
		t.Fatalf("半径 0 应注销, Len=%d", c.Len())
	}
}

// 空索引是零开销快路径：位移原样返回，不引入任何接触。
func TestIndexSlideEmptyIndexPassesThrough(t *testing.T) {
	c := NewIndex()
	x, y, hits := c.Slide(1.5, 2.5, 0.5, -0.25, 0.2)
	if hits != 0 || math.Abs(x-2.0) > 1e-12 || math.Abs(y-2.25) > 1e-12 {
		t.Fatalf("空索引应原样位移, got (%.6f,%.6f) hits=%d", x, y, hits)
	}
	var nilIndex *Index
	if x, y, _ := nilIndex.Slide(1, 2, 0.5, 0, 0.2); x != 1.5 || y != 2 {
		t.Fatalf("nil 索引应原样位移, got (%.3f,%.3f)", x, y)
	}
}

// 正面撞圆柱：停在半径和处（而不是穿过或停在格子边界）。
func TestIndexSlideStopsAtCylinder(t *testing.T) {
	c := NewIndex()
	c.Set(ecs.Entity(7), 5.5, 4.5, 0.18)
	x, y, hits := c.Slide(4.5, 4.5, 1.0, 0, 0.2)
	if hits != 1 {
		t.Fatalf("应有 1 次接触, got %d", hits)
	}
	if math.Abs(x-(5.5-0.18-0.2)) > 2e-3 || math.Abs(y-4.5) > 1e-9 {
		t.Fatalf("应停在树干边缘 (5.119,4.5), got (%.6f,%.6f)", x, y)
	}
}

// --- 动态层（移动实体间的碰撞）---

// 动态体挡住移动体：两个圆柱，一个静止的"动物"，一个走过来的"玩家"。
func TestDynamicBlocksMover(t *testing.T) {
	c := NewIndex()
	// 动物：格心圆柱 r=0.3 放在 (5.5, 5.5)
	c.SetDynamic(1, 5.5, 5.5, 0.3, 0, 0, 0)
	// 玩家 r=0.305 从 (3.5,5.5) 沿 +X 走 3 格
	ex, _, hits := c.SlideBodyExcept(Body{X: 3.5, Z: 5.5, Radius: 0.305}, 3.0, 0, 2)
	if hits == 0 {
		t.Fatalf("hits = 0, 期望撞上动物")
	}
	// 接触距离 = 0.305 + 0.3 = 0.605 -> 停在 5.5-0.605 = 4.895
	if diff := ex - 4.895; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("x = %.6f, want 4.895000（撞在动物表面上）", ex)
	}
}

// 自己不能挡住自己：动态层里也有本实体，但必须被排除。
func TestDynamicExcludesSelf(t *testing.T) {
	c := NewIndex()
	c.SetDynamic(7, 5.5, 5.5, 0.3, 0, 0, 0)
	// 实体 7 从自己所在位置出发向外走：不该被自己挡住
	ex, _, hits := c.SlideBodyExcept(Body{X: 5.5, Z: 5.5, Radius: 0.305}, 2.0, 0, 7)
	if hits != 0 {
		t.Fatalf("hits = %d, want 0（不该撞上自己）", hits)
	}
	if diff := ex - 7.5; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("x = %.6f, want 7.5（畅通无阻）", ex)
	}
}

// 静态 + 动态两层都要解算：先被树挡住滑开，再撞上动物。
func TestStaticAndDynamicBothResolve(t *testing.T) {
	c := NewIndex()
	c.Set(1, 4.5, 5.5, 0.18)                // 静态树
	c.SetDynamic(2, 6.5, 5.5, 0.3, 0, 0, 0) // 动态动物
	ex, _, hits := c.SlideBodyExcept(Body{X: 3.5, Z: 5.5, Radius: 0.305}, 4.0, 0, 9)
	if hits == 0 {
		t.Fatalf("hits = 0, 期望撞到树和/或动物")
	}
	if ex > 6.5-0.605+1e-6 {
		t.Fatalf("x = %.6f, 不该穿过动物（上限 %.6f）", ex, 6.5-0.605)
	}
}

// 动态层的形状跟随位置更新（Update 复用句柄）。
func TestDynamicUpdatesPosition(t *testing.T) {
	c := NewIndex()
	c.SetDynamic(1, 5.5, 5.5, 0.3, 0, 0, 0)
	if c.DynamicLen() != 1 {
		t.Fatalf("DynamicLen = %d, want 1", c.DynamicLen())
	}
	// 把动物挪走
	c.SetDynamic(1, 20.5, 20.5, 0.3, 0, 0, 0)
	if c.DynamicLen() != 1 {
		t.Fatalf("更新后 DynamicLen = %d, want 1（句柄复用，不新增）", c.DynamicLen())
	}
	// 原来位置不再被挡
	ex, _, hits := c.SlideBodyExcept(Body{X: 3.5, Z: 5.5, Radius: 0.305}, 3.0, 0, 2)
	if hits != 0 || ex < 6.4 {
		t.Fatalf("x=%.4f hits=%d, 动物挪走后应畅通", ex, hits)
	}
}

// Reset 只清静态层，动态层（动物）必须保留——否则读档/换图会把动物形状清掉。
func TestResetKeepsDynamicLayer(t *testing.T) {
	c := NewIndex()
	c.Set(1, 4.5, 4.5, 0.18)
	c.SetDynamic(2, 5.5, 5.5, 0.3, 0, 0, 0)
	c.Reset()
	if c.Len() != 0 {
		t.Fatalf("Reset 后静态数 = %d, want 0", c.Len())
	}
	if c.DynamicLen() != 1 {
		t.Fatalf("Reset 后动态数 = %d, want 1（动态层不该被清）", c.DynamicLen())
	}
}

// 注销动态体。
func TestClearDynamic(t *testing.T) {
	c := NewIndex()
	c.SetDynamic(1, 5.5, 5.5, 0.3, 0, 0, 0)
	c.ClearDynamic(1)
	if c.DynamicLen() != 0 {
		t.Fatalf("DynamicLen = %d, want 0", c.DynamicLen())
	}
	c.ClearDynamic(1) // 幂等
}

// 胶囊动态体（四足动物：沿朝向铺开）。
func TestDynamicCapsuleBlocks(t *testing.T) {
	c := NewIndex()
	// 狼：半径 0.246，半长 1.057，朝向 +X -> 段从 (4.443,5.5) 到 (6.557,5.5)
	c.SetDynamic(1, 5.5, 5.5, 0.246, 1.057, 1, 0)
	ex, _, hits := c.SlideBodyExcept(Body{X: 1.5, Z: 5.5, Radius: 0.305}, 6.0, 0, 2)
	if hits == 0 {
		t.Fatalf("hits = 0, 期望撞上狼的胶囊")
	}
	// 段端 4.443 - 0.305 - 0.246 = 3.892
	if diff := ex - 3.892; diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("x = %.6f, want 3.892000", ex)
	}
}

// --- 按运动类别分侧（三阶段移动的基础）---

// SlideStatic 只被静态体挡住：动态邻居不再是硬碰撞（那是 ORCA 的事）。
func TestSlideStaticIgnoresDynamic(t *testing.T) {
	c := NewIndex()
	c.Set(1, 5.5, 5.5, 0.28)                // 静态树
	c.SetDynamic(2, 5.5, 8.5, 0.3, 0, 0, 0) // 动态动物（不在路径上）
	// 走向树：仍被静态体挡住
	ex, _, hits := c.SlideStatic(Body{X: 3.5, Z: 5.5, Radius: 0.305}, 3.0, 0)
	if hits == 0 {
		t.Fatal("静态树应挡住")
	}
	if diff := ex - (5.5 - 0.28 - 0.305); diff > 1e-6 || diff < -1e-6 {
		t.Fatalf("x = %.6f, want %.6f", ex, 5.5-0.28-0.305)
	}
}

// 动态邻居存在时，SlideStatic 完全无视它（直接走过去）。
func TestSlideStaticPassesThroughDynamic(t *testing.T) {
	c := NewIndex()
	c.SetDynamic(2, 5.5, 5.5, 0.3, 0, 0, 0) // 动物正好挡在路中间
	ex, _, hits := c.SlideStatic(Body{X: 3.5, Z: 5.5, Radius: 0.305}, 3.0, 0)
	if hits != 0 {
		t.Fatalf("SlideStatic 不该被动态体挡住, hits=%d", hits)
	}
	if diff := ex - 6.5; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("x = %.6f, want 6.5（穿过去，交给 ORCA 处理）", ex)
	}
}

// Neighbors 只返回动态体，且排除自己，并按实体 id 排序（确定性）。
func TestNeighborsOnlyDynamicSorted(t *testing.T) {
	c := NewIndex()
	c.Set(9, 5.5, 5.5, 0.28) // 静态树：不该出现在邻居里
	c.SetDynamic(5, 6.0, 5.5, 0.3, 0, 0, 0)
	c.SetDynamic(3, 5.5, 6.0, 0.3, 0, 0, 0)
	c.SetDynamic(7, 5.0, 5.5, 0.3, 0, 0, 0) // 自己

	ns := c.Neighbors(5.0, 5.5, 3.0, 7)
	if len(ns) != 2 {
		t.Fatalf("邻居数 = %d, want 2（不含静态、不含自己）: %+v", len(ns), ns)
	}
	if ns[0].Entity != 3 || ns[1].Entity != 5 {
		t.Fatalf("邻居应按 id 排序，got %d, %d", ns[0].Entity, ns[1].Entity)
	}
}

// 胶囊邻居（四足动物）要能还原出半径与轴向。
func TestNeighborsCapsuleGeometry(t *testing.T) {
	c := NewIndex()
	c.SetDynamic(1, 5.5, 5.5, 0.246, 1.057, 1, 0) // 沿 +X 的狼
	c.SetDynamic(2, 2.0, 2.0, 0.305, 0, 0, 0)     // 自己（远处）

	ns := c.Neighbors(5.5, 5.5, 3.0, 2)
	if len(ns) != 1 {
		t.Fatalf("邻居数 = %d, want 1", len(ns))
	}
	n := ns[0]
	if math.Abs(n.Radius-0.246) > 1e-9 || math.Abs(n.HalfLength-1.057) > 1e-9 {
		t.Fatalf("胶囊参数不符: %+v", n)
	}
	if math.Abs(math.Abs(n.FaceX)-1) > 1e-9 || math.Abs(n.FaceZ) > 1e-9 {
		t.Fatalf("轴向应沿 X: %+v", n)
	}
}
