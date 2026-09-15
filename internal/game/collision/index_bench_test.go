package collision

import (
	"fmt"
	"testing"

	"starve/internal/ecs"
)

// benchWorld 造一个 count 个格心圆柱的世界（间距 3 格，模拟森林图密度）。
func benchWorld(count int) *Index {
	c := NewIndex()
	side := 1
	for side*side < count {
		side++
	}
	for i := 0; i < count; i++ {
		x := float64((i%side)*3) + 0.5
		y := float64((i/side)*3) + 0.5
		c.Set(ecs.Entity(i+1), x, y, 0.18)
	}
	return c
}

// BenchmarkSlide 测"一次移动 tick 的扫掠滑动"成本：每 tick 每个可移动实体一次。
// 20Hz × 100 个可移动实体时，单次 1µs 也只有 2ms/秒，留足余量。
func BenchmarkSlide(b *testing.B) {
	for _, n := range []int{200, 2000, 20000} {
		b.Run(fmt.Sprintf("solids=%d", n), func(b *testing.B) {
			c := benchWorld(n)
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				// 贴着最近一棵树斜着推进：命中 + 切面滑动（最贵的那条路径）
				c.Slide(0.2, 0.4, 0.25, 0.25, 0.2)
			}
		})
	}
}

// BenchmarkSlideOpenField 空旷处的快路径（无接触）：只有宽阶段。
func BenchmarkSlideOpenField(b *testing.B) {
	c := benchWorld(2000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Slide(1000.5, 1000.5, 0.5, 0, 0.2)
	}
}
