package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// 守卫踩过的坑：Go 里把多个字段写成共用同一个 json tag 时，
// 这些字段会因名字冲突而**整体不出现在输出里**（曾经导致接触点坐标丢失）。
func TestVec3JSONHasCoordinates(t *testing.T) {
	b, err := json.Marshal(vec3j{X: 1, Y: 2, Z: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"x":`, `"y":`, `"z":`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("vec3j JSON 缺少 %s：%s", key, b)
		}
	}
}

func TestSimContactJSONHasCoordinates(t *testing.T) {
	b, err := json.Marshal(simContact{X: 1, Y: 2, Z: 3, R: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"x":`, `"y":`, `"z":`, `"r":`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("simContact JSON 缺少 %s：%s", key, b)
		}
	}
}

// 两个重叠的球步进一次：必须产出带坐标的接触点。
func TestSimStepEmitsContactWithPosition(t *testing.T) {
	in := simIn{
		Bodies: []simBody{
			{P: vec3j{X: 0, Y: 1.0, Z: 0}, R: 0.5},
			{P: vec3j{X: 0, Y: 0.5, Z: 0}, R: 0.5},
		},
		Dt: 1.0 / 60, Gravity: 22, FloorY: 0, Bound: 3, Rest: 0.2, Friction: 0.05,
	}
	out := simStep(in)

	sphereContact := false
	for _, c := range out.Contacts {
		if c.Y > 0.2 && c.Y < 0.9 { // 球-球接触点落在两球之间（地板接触点在 y=0）
			sphereContact = true
		}
	}
	if !sphereContact {
		t.Fatalf("expected a sphere-sphere contact, got %+v", out.Contacts)
	}

	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"x":`) || !strings.Contains(string(b), `"contacts":`) {
		t.Fatalf("contacts JSON 缺少坐标：%s", b)
	}
}

// 基础页的查询结果同样必须带坐标（closest / contact）。
func TestComputeReturnsCoordinates(t *testing.T) {
	sc := scene{
		Mode:   "sphereAABB",
		Sphere: &spherej{C: vec3j{X: 2, Y: 0.5, Z: 0.5}, R: 1.1},
		AABB:   &aabbj{Min: vec3j{}, Max: vec3j{X: 1, Y: 1, Z: 1}},
	}
	res := compute(sc)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if res.Contact == nil {
		t.Fatal("expected contact info for overlapping sphere/box")
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"closest":{`, `"normal":{`, `"point":{`} {
		if !strings.Contains(string(b), key) {
			t.Fatalf("result JSON 缺少 %s：%s", key, b)
		}
	}
}

// 投射场景：一组混合目标里，必须取“最早命中”的那个（这里胶囊比远端的盒子近）。
func TestCastScenePicksEarliest(t *testing.T) {
	in := castIn{
		Sphere: spherej{C: vec3j{X: -5, Y: 1, Z: 0}, R: 0.25},
		Motion: vec3j{X: 10, Y: 0, Z: 0},
		Targets: []castTarget{
			{Kind: "aabb", Min: &vec3j{X: 5, Y: 0, Z: -1}, Max: &vec3j{X: 5.4, Y: 2, Z: 1}},
			{Kind: "capsule", A: &vec3j{X: 0, Y: 0, Z: 0}, B: &vec3j{X: 0, Y: 2, Z: 0}, R: 0.4},
		},
	}
	out := castScene(in)
	if !out.Hit {
		t.Fatal("should hit something")
	}
	if out.Best.Index != 1 {
		t.Fatalf("best index = %d, want 1（胶囊更近）", out.Best.Index)
	}
	if !strings.Contains(string(mustMarshal(t, out)), `"x":`) {
		t.Fatal("cast result missing coordinates")
	}
}

// 中间有墙时：子弹应先命中墙（被挡住），而不是后面的目标。
func TestCastSceneBlockedByWall(t *testing.T) {
	wallMin := vec3j{X: -1.2, Y: 0, Z: -1}
	wallMax := vec3j{X: -0.8, Y: 2, Z: 1}
	in := castIn{
		Sphere: spherej{C: vec3j{X: -5, Y: 1, Z: 0}, R: 0.25},
		Motion: vec3j{X: 10, Y: 0, Z: 0},
		Targets: []castTarget{
			{Kind: "wall", Min: &wallMin, Max: &wallMax}, // 未知 kind 会被忽略，用于确认健壮性
			{Kind: "capsule", A: &vec3j{X: 0, Y: 0, Z: 0}, B: &vec3j{X: 0, Y: 2, Z: 0}, R: 0.4},
			{Kind: "aabb", Min: &wallMin, Max: &wallMax}, // 真正参与判定的墙
		},
	}
	out := castScene(in)
	if !out.Hit {
		t.Fatal("should hit the wall")
	}
	if out.Best.Index != 2 || out.Best.Kind != "aabb" {
		t.Fatalf("best = %+v, want index 2 (aabb 墙)", out.Best)
	}
	// 子弹中心走到 x = -1.2 - 0.25 = -1.45 时触墙 => t = 0.355
	if math.Abs(out.Best.T-0.355) > 1e-3 {
		t.Fatalf("wall hit T = %v, want ~0.355", out.Best.T)
	}
	// 后面的胶囊也有命中记录（说明是“取最早”而不是“只测一个”）
	if len(out.All) < 2 {
		t.Fatalf("expected multiple hits recorded, got %d", len(out.All))
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 劈砍：刀是 OBB，按角度细分扫掠。
func TestSwingSceneHitsBody(t *testing.T) {
	blade := swingBlade{
		Origin:     vec3j{X: -2.6, Y: 1.2, Z: 0},
		Length:     2.6,
		HalfHeight: 0.05,
		HalfWidth:  0.12,
	}
	body := castTarget{Kind: "capsule", A: &vec3j{X: 0.1, Y: 0, Z: 0}, B: &vec3j{X: 0.1, Y: 1.8, Z: 0}, R: 0.4}
	out := swingScene(swingIn{Blade: blade, AngleFrom: 1.25, AngleTo: -0.55, SubSteps: 12, Targets: []castTarget{body}})
	if !out.Hit {
		t.Fatal("swing should hit the body")
	}
	if out.Best.Index != 0 || out.Best.Kind != "capsule" {
		t.Fatalf("best = %+v, want index 0 capsule", out.Best)
	}
	if out.Best.Depth <= 0 {
		t.Fatalf("Depth = %v, want > 0", out.Best.Depth)
	}
}

// 中间有墙：刀应先砍在墙上，目标不受影响。
func TestSwingSceneBlockedByWall(t *testing.T) {
	blade := swingBlade{
		Origin:     vec3j{X: -2.6, Y: 1.2, Z: 0},
		Length:     2.6,
		HalfHeight: 0.05,
		HalfWidth:  0.12,
	}
	body := castTarget{Kind: "capsule", A: &vec3j{X: 0.1, Y: 0, Z: 0}, B: &vec3j{X: 0.1, Y: 1.8, Z: 0}, R: 0.4}
	wall := castTarget{Kind: "aabb", Min: &vec3j{X: -1.5, Y: 0, Z: -2.6}, Max: &vec3j{X: -1.3, Y: 2.4, Z: 2.6}}
	out := swingScene(swingIn{Blade: blade, AngleFrom: 1.25, AngleTo: -0.55, SubSteps: 12, Targets: []castTarget{body, wall}})
	if !out.Hit {
		t.Fatal("swing should hit the wall")
	}
	if out.Best.Index != 1 || out.Best.Kind != "aabb" {
		t.Fatalf("best = %+v, want index 1 aabb（先砍到墙）", out.Best)
	}
	// 墙在 x ∈ [-1.5, -1.3]，接触点应落在墙体内（细分步长决定了它可能略微深入）
	if out.Best.Point.X < -1.51 || out.Best.Point.X > -1.29 {
		t.Fatalf("wall contact x = %v, want within [-1.5,-1.3]", out.Best.Point.X)
	}
}
