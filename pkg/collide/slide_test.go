package collide

import (
	"math"
	"testing"
)

// slideEngine 建一个只放静态障碍的引擎（BVH 宽阶段，和世界侧一致）。
func slideEngine(t *testing.T, shapes ...Solid) *Engine {
	t.Helper()
	e := NewEngine(EngineOptions{Margin: 0, Scanner: NewBVHScanner()})
	for _, s := range shapes {
		e.Add(s)
	}
	return e
}

// 竖直圆柱（树干）：轴在 (x, y, z)，半径 r，高度 h。
func trunk(x, y, z, r, h float64) Capsule {
	return Capsule{
		A: Vec3{X: x, Y: y, Z: z},
		B: Vec3{X: x, Y: y + h, Z: z},
		R: r,
	}
}

func TestSweepSlideHeadOnStops(t *testing.T) {
	// 树干半径 0.25 + 身体半径 0.25 → 圆心最近能到 0.5
	eng := slideEngine(t, trunk(1, 0, 0, 0.25, 3))
	res := eng.SweepSlideSphere(Sphere{C: Vec3{}, R: 0.25}, Vec3{X: 1}, nil, SlideOptions{})
	if res.Hits != 1 || !res.Blocked {
		t.Fatalf("正面撞树干应记 1 次接触, got hits=%d blocked=%v", res.Hits, res.Blocked)
	}
	if math.Abs(res.Moved.X-0.5) > 2e-3 {
		t.Fatalf("应停在 x≈0.5, got %.6f", res.Moved.X)
	}
	if math.Abs(res.Moved.Z) > 1e-9 {
		t.Fatalf("正面撞不应产生横向位移, got z=%.9f", res.Moved.Z)
	}
}

func TestSweepSlideSlidesAroundTrunk(t *testing.T) {
	eng := slideEngine(t, trunk(1, 0, 0, 0.25, 3))
	// 偏心正撞：法向分量被吃掉，切向分量保留 → 绕树滑走
	res := eng.SweepSlideSphere(Sphere{C: Vec3{Z: 0.3}, R: 0.25}, Vec3{X: 1}, nil, SlideOptions{})
	if res.Hits == 0 {
		t.Fatal("偏心撞树干应发生接触")
	}
	if res.Moved.Z < 0.1 {
		t.Fatalf("切向位移应保留（绕树滑走），got z=%.6f", res.Moved.Z)
	}
	// 最终不得嵌进树干：圆心到轴的平面距离 ≥ 0.5（0.3 是起点偏移）
	dist := math.Hypot(res.Moved.X-1, 0.3+res.Moved.Z)
	if dist < 0.5-1e-6 {
		t.Fatalf("滑动后嵌入树干: 距离 %.9f < 0.5", dist)
	}
}

func TestSweepSlideAlongWallKeepsTangential(t *testing.T) {
	// 一堵南北向的墙：x ∈ [2,3]，z ∈ [-5,5]
	wall := AABB{Min: Vec3{X: 2, Y: 0, Z: -5}, Max: Vec3{X: 3, Y: 2, Z: 5}}
	eng := slideEngine(t, wall)
	res := eng.SweepSlideSphere(
		Sphere{C: Vec3{X: 0, Y: 1}, R: 0.25},
		Vec3{X: 2, Z: 1},
		nil, SlideOptions{},
	)
	if !res.Blocked {
		t.Fatal("撞墙应记接触")
	}
	if math.Abs(res.Moved.X-1.75) > 2e-3 {
		t.Fatalf("应停在墙面前 x≈1.75, got %.6f", res.Moved.X)
	}
	// 切向（z）位移是纯滑动分量，应完整走完
	if math.Abs(res.Moved.Z-1) > 3e-3 {
		t.Fatalf("墙前应滑满切向位移 z≈1, got %.6f", res.Moved.Z)
	}
}

func TestSweepSlideCornerStopsBothAxes(t *testing.T) {
	// 两面墙夹出内角（+x 与 +z 都堵）
	eng := slideEngine(t,
		AABB{Min: Vec3{X: 2, Y: 0, Z: -5}, Max: Vec3{X: 3, Y: 2, Z: 5}},
		AABB{Min: Vec3{X: -5, Y: 0, Z: 2}, Max: Vec3{X: 5, Y: 2, Z: 3}},
	)
	res := eng.SweepSlideSphere(
		Sphere{C: Vec3{X: 1, Y: 1, Z: 1}, R: 0.25},
		Vec3{X: 1, Z: 1},
		nil, SlideOptions{},
	)
	if res.Hits < 2 {
		t.Fatalf("墙角应发生两次接触, got %d", res.Hits)
	}
	if math.Abs(res.Moved.X-0.75) > 4e-3 || math.Abs(res.Moved.Z-0.75) > 4e-3 {
		t.Fatalf("墙角应两轴都停在墙面前, got (%.6f, %.6f)", res.Moved.X, res.Moved.Z)
	}
}

// 8 向输入正对角撞盒子的角：法向与位移正好反向、切面投影为 0，
// 这时不能钉死——墙角兜底沿"最浅穿透面"推出，于是顺着墙面滑过去。
func TestSweepSlideCornerAssistSlidesAlongFace(t *testing.T) {
	box := AABB{Min: Vec3{X: 4, Y: 0, Z: 4}, Max: Vec3{X: 5, Y: 2, Z: 5}}
	eng := slideEngine(t, box)
	mover := Sphere{C: Vec3{X: 3.858, Y: 1, Z: 3.858}, R: 0.2}
	res := eng.SweepSlideSphere(mover, Vec3{X: 0.354, Z: 0.354}, nil, SlideOptions{})
	end := mover.C.Add(res.Moved)
	if math.Abs(end.X-(4-0.2)) > 1e-6 {
		t.Fatalf("应贴住盒子的西面 x=3.8, got %.6f", end.X)
	}
	if end.Z <= 3.87 {
		t.Fatalf("应沿墙面往前滑（z 增加），got z=%.6f", end.Z)
	}
	// 不得嵌进盒子：中心不在盒内
	if end.X > 4 && end.Z > 4 {
		t.Fatalf("滑动后嵌进盒子: (%f,%f)", end.X, end.Z)
	}
}

// 兜底只在"完全被挡住"时用，且步长固定：正面顶住圆柱（树）时不会穿到另一侧。
func TestSweepSlideCornerAssistDoesNotTunnelCylinder(t *testing.T) {
	eng := slideEngine(t, trunk(1, 0, 0, 0.25, 3)) // 半径和 0.5
	mover := Sphere{C: Vec3{X: 0.5}, R: 0.25}      // 正好贴住
	res := eng.SweepSlideSphere(mover, Vec3{X: 0.5}, nil, SlideOptions{})
	end := mover.C.Add(res.Moved)
	if end.X > 0.51 {
		t.Fatalf("正面顶树不应穿到另一侧, got x=%.6f", end.X)
	}
	if math.Abs(end.Z) > 1e-9 {
		t.Fatalf("正面顶树不应横移, got z=%.6f", end.Z)
	}
}

// 正对平面推：最浅穿透面就是该平面，结果仍然是停住（墙角兜底不改变正面撞墙语义）。
func TestSweepSlideFaceStillStops(t *testing.T) {
	box := AABB{Min: Vec3{X: 4, Y: 0, Z: 4}, Max: Vec3{X: 5, Y: 2, Z: 5}}
	eng := slideEngine(t, box)
	mover := Sphere{C: Vec3{X: 3.8, Y: 1, Z: 4.5}, R: 0.2}
	res := eng.SweepSlideSphere(mover, Vec3{X: 0.5}, nil, SlideOptions{})
	end := mover.C.Add(res.Moved)
	if math.Abs(end.X-3.8) > 2e-3 || math.Abs(end.Z-4.5) > 1e-9 {
		t.Fatalf("正面撞墙应停在 x≈3.8 且不横移, got (%.6f,%.6f)", end.X, end.Z)
	}
}

func TestSweepSlideNoObstacleKeepsMotion(t *testing.T) {
	eng := slideEngine(t, trunk(10, 0, 10, 0.25, 3))
	motion := Vec3{X: 0.3, Z: -0.2}
	res := eng.SweepSlideSphere(Sphere{C: Vec3{}, R: 0.25}, motion, nil, SlideOptions{})
	if res.Hits != 0 || res.Blocked {
		t.Fatalf("空旷处不应有接触, got hits=%d blocked=%v", res.Hits, res.Blocked)
	}
	if res.Moved.Sub(motion).Len() > 1e-12 {
		t.Fatalf("空旷处位移应等于请求位移, got %+v", res.Moved)
	}
}

func TestSweepSlideDepenetratesOverlap(t *testing.T) {
	// 圆心与树心重合（例如读档/传送落在树干里）：应按深度推出，不能留在里面
	eng := slideEngine(t, trunk(0, 0, 0, 0.25, 3))
	res := eng.SweepSlideSphere(Sphere{C: Vec3{X: 0.05, Z: 0.05}, R: 0.25}, Vec3{X: 0.2}, nil, SlideOptions{})
	end := Vec3{X: 0.05, Z: 0.05}.Add(res.Moved)
	dist := math.Hypot(end.X, end.Z) // 最终圆心到树干轴（原点）的平面距离
	if dist < 0.5 {
		t.Fatalf("重叠后应被推出到 0.5 以外, got %.6f", dist)
	}
}

func TestSweepSlideDoesNotTunnel(t *testing.T) {
	// 高速位移（10 格）穿过薄墙：连续扫掠必须挡住，不允许穿过去
	wall := AABB{Min: Vec3{X: 2, Y: 0, Z: -5}, Max: Vec3{X: 2.4, Y: 2, Z: 5}}
	eng := slideEngine(t, wall)
	res := eng.SweepSlideSphere(Sphere{C: Vec3{}, R: 0.25}, Vec3{X: 10}, nil, SlideOptions{})
	end := Vec3{}.Add(res.Moved)
	if end.X > 2-0.25+1e-6 {
		t.Fatalf("高速移动穿墙了: x=%.6f", end.X)
	}
	if !res.Blocked {
		t.Fatal("高速撞墙应记接触")
	}
}
