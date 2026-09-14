package collision

import (
	"math"
	"testing"

	"starve/internal/ecs"
)

// 注册/更新/注销/重建：句柄与计数跟随实体，重复操作幂等。
func TestWorldSetClearReset(t *testing.T) {
	c := NewWorld()
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
func TestWorldZeroRadiusClears(t *testing.T) {
	c := NewWorld()
	c.Set(1, 1.5, 1.5, 0.2)
	c.Set(1, 1.5, 1.5, 0)
	if c.Len() != 0 {
		t.Fatalf("半径 0 应注销, Len=%d", c.Len())
	}
}

// 空索引是零开销快路径：位移原样返回，不引入任何接触。
func TestWorldSlideEmptyIndexPassesThrough(t *testing.T) {
	c := NewWorld()
	x, y, hits := c.Slide(1.5, 2.5, 0.5, -0.25, 0.2)
	if hits != 0 || math.Abs(x-2.0) > 1e-12 || math.Abs(y-2.25) > 1e-12 {
		t.Fatalf("空索引应原样位移, got (%.6f,%.6f) hits=%d", x, y, hits)
	}
	var nilWorld *World
	if x, y, _ := nilWorld.Slide(1, 2, 0.5, 0, 0.2); x != 1.5 || y != 2 {
		t.Fatalf("nil 索引应原样位移, got (%.3f,%.3f)", x, y)
	}
}

// 正面撞圆柱：停在半径和处（而不是穿过或停在格子边界）。
func TestWorldSlideStopsAtCylinder(t *testing.T) {
	c := NewWorld()
	c.Set(ecs.Entity(7), 5.5, 4.5, 0.18)
	x, y, hits := c.Slide(4.5, 4.5, 1.0, 0, 0.2)
	if hits != 1 {
		t.Fatalf("应有 1 次接触, got %d", hits)
	}
	if math.Abs(x-(5.5-0.18-0.2)) > 2e-3 || math.Abs(y-4.5) > 1e-9 {
		t.Fatalf("应停在树干边缘 (5.119,4.5), got (%.6f,%.6f)", x, y)
	}
}
