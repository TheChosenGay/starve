package collide

import (
	"fmt"
	"math"
	"testing"
)

// benchWorld 造一个 n 个胶囊的世界（确定性散布），返回引擎与图元切片。
func benchWorld(b *testing.B, n int, margin float64) (*Engine, []Capsule) {
	b.Helper()
	e := NewEngine(EngineOptions{Margin: margin})
	e.Reserve(n)
	bodies := make([]Capsule, n)
	const side = 120.0 // 世界半边长（米）
	for i := 0; i < n; i++ {
		// 黄金角螺旋：均匀铺开，密度与真实场景同量级
		r := side * math.Sqrt(float64(i)/float64(n))
		a := float64(i) * 2.399963
		x, z := r*math.Cos(a), r*math.Sin(a)
		bodies[i] = Capsule{
			A: Vec3{X: x, Z: z},
			B: Vec3{X: x, Y: 1.8, Z: z},
			R: 0.4,
		}
		e.Add(bodies[i])
	}
	return e, bodies
}

// benchQueries 造 q 个查询球（确定性）。
func benchQueries(q int, radius float64) []Sphere {
	out := make([]Sphere, q)
	for i := range out {
		a := float64(i) * 1.7
		out[i] = Sphere{
			C: Vec3{X: 40 * math.Cos(a), Y: 0.9, Z: 40 * math.Sin(a)},
			R: radius,
		}
	}
	return out
}

// BenchmarkBroadQueryNaive：不用扫描器——每次查询把全部物体喂给窄阶段。
func BenchmarkBroadQueryNaive(b *testing.B) {
	for _, n := range []int{200, 2000, 20000} {
		_, bodies := benchWorld(b, n, 0.25)
		queries := benchQueries(32, 1.6)
		b.Run(fmt.Sprintf("n%d", n), func(b *testing.B) {
			b.ReportAllocs()
			hits := 0
			for i := 0; i < b.N; i++ {
				for _, q := range queries {
					for j := range bodies {
						if TestShapes(q, bodies[j]) {
							hits++
						}
					}
				}
			}
			_ = hits
		})
	}
}

// BenchmarkBroadQueryScanner：扫描器先做 AABB 剔除，只有候选进窄阶段。
func BenchmarkBroadQueryScanner(b *testing.B) {
	for _, n := range []int{200, 2000, 20000} {
		e, _ := benchWorld(b, n, 0.25)
		queries := benchQueries(32, 1.6)
		b.Run(fmt.Sprintf("n%d", n), func(b *testing.B) {
			b.ReportAllocs()
			hits, cand := 0, 0
			for i := 0; i < b.N; i++ {
				for _, q := range queries {
					e.Query(q.Bounds(), func(h Handle) bool {
						cand++
						if TestShapes(q, e.Shape(h)) {
							hits++
						}
						return true
					})
				}
			}
			_ = hits
			_ = cand
		})
	}
}

// BenchmarkEngineUpdate 对比"增量更新"与"每帧全量重建"的索引维护成本。
func BenchmarkEngineUpdate(b *testing.B) {
	const n = 2000
	for _, dirty := range []float64{0.05, 1.0} {
		e, bodies := benchWorld(b, n, 0.25)
		handles := make([]Handle, 0, n)
		for i := 0; i < n; i++ {
			handles = append(handles, makeHandle(uint32(i), 0))
		}
		b.Run(fmt.Sprintf("incremental_%.0f%%", dirty*100), func(b *testing.B) {
			b.ReportAllocs()
			moving := int(float64(n) * dirty)
			for i := 0; i < b.N; i++ {
				dx := float64(i%17) * 0.01
				for j := 0; j < moving; j++ {
					m := bodies[j]
					m.A.X += dx
					m.B.X += dx
					e.Update(handles[j], m)
				}
			}
		})
	}
	b.Run("rebuild_all", func(b *testing.B) {
		e, _ := benchWorld(b, n, 0.25)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			e.Rebuild()
		}
	})
}
