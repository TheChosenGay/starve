package modelcollide

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// buildGLB 造一个最小 .glb 用于测试：一个节点 + 一个网格（POSITION 属性），
// 可选节点 TRS 与蒙皮（关节平移 + 权重 1）。
type glbFixture struct {
	Positions   [][3]float32
	Scale       [3]float64
	Translate   [3]float64
	SkinJointAt [3]float64 // 非零则生成一个蒙皮：单个关节带这个平移，权重全 1
}

func buildGLB(t *testing.T, f glbFixture) string {
	t.Helper()
	positions := make([]float32, 0, len(f.Positions)*3)
	minV := [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	maxV := [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, p := range f.Positions {
		positions = append(positions, p[0], p[1], p[2])
		for i := 0; i < 3; i++ {
			v := float64(p[i])
			minV[i] = math.Min(minV[i], v)
			maxV[i] = math.Max(maxV[i], v)
		}
	}
	bin := floatsToBytes(positions)
	views := []map[string]any{
		{"buffer": 0, "byteOffset": 0, "byteLength": len(positions) * 4},
	}
	accessors := []map[string]any{
		{
			"bufferView": 0, "componentType": 5126, "count": len(f.Positions), "type": "VEC3",
			"min": []float64{minV[0], minV[1], minV[2]},
			"max": []float64{maxV[0], maxV[1], maxV[2]},
		},
	}
	attributes := map[string]any{"POSITION": 0}
	node := map[string]any{"mesh": 0}
	doc := map[string]any{
		"asset":  map[string]any{"version": "2.0"},
		"scene":  0,
		"scenes": []map[string]any{{"nodes": []int{0}}},
		"meshes": []map[string]any{{"primitives": []map[string]any{{"attributes": attributes}}}},
		"nodes":  []map[string]any{node},
	}
	if f.Scale != [3]float64{} || f.Translate != [3]float64{} {
		scale := f.Scale
		if scale == [3]float64{} {
			scale = [3]float64{1, 1, 1}
		}
		node["scale"] = scale[:]
		node["translation"] = f.Translate[:]
	}
	if f.SkinJointAt != [3]float64{} {
		offset := len(bin)
		joints := make([]float32, 0, len(f.Positions)*4)
		weights := make([]float32, 0, len(f.Positions)*4)
		for range f.Positions {
			joints = append(joints, 0, 0, 0, 0)
			weights = append(weights, 1, 0, 0, 0)
		}
		bin = append(bin, floatsToBytes(joints)...)
		bin = append(bin, floatsToBytes(weights)...)
		ibm := []float64{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
		views = append(views,
			map[string]any{"buffer": 0, "byteOffset": offset, "byteLength": len(joints) * 4},
			map[string]any{"buffer": 0, "byteOffset": offset + len(joints)*4, "byteLength": len(weights) * 4},
			map[string]any{"buffer": 0, "byteOffset": offset + (len(joints)+len(weights))*4, "byteLength": 64},
		)
		accessors = append(accessors,
			map[string]any{"bufferView": 1, "componentType": 5126, "count": len(f.Positions), "type": "VEC4"},
			map[string]any{"bufferView": 2, "componentType": 5126, "count": len(f.Positions), "type": "VEC4"},
			map[string]any{"bufferView": 3, "componentType": 5126, "count": 1, "type": "MAT4"},
		)
		bin = append(bin, floatsToBytes(float32s(ibm))...)
		attributes["JOINTS_0"] = 1
		attributes["WEIGHTS_0"] = 2
		doc["skins"] = []map[string]any{{"joints": []int{1}, "inverseBindMatrices": 3}}
		node["skin"] = 0
		doc["nodes"] = []map[string]any{
			node,
			{"translation": f.SkinJointAt[:], "children": []int{}},
		}
		doc["scenes"] = []map[string]any{{"nodes": []int{0, 1}}}
	}
	doc["bufferViews"] = views
	doc["accessors"] = accessors
	doc["buffers"] = []map[string]any{{"byteLength": len(bin)}}

	jsonChunk, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for len(jsonChunk)%4 != 0 {
		jsonChunk = append(jsonChunk, ' ')
	}
	for len(bin)%4 != 0 {
		bin = append(bin, 0)
	}
	var out []byte
	out = append(out, "glTF"...)
	out = appendU32(out, 2)
	out = appendU32(out, uint32(12+8+len(jsonChunk)+8+len(bin)))
	out = appendU32(out, uint32(len(jsonChunk)))
	out = appendU32(out, 0x4E4F534A)
	out = append(out, jsonChunk...)
	out = appendU32(out, uint32(len(bin)))
	out = appendU32(out, 0x004E4942)
	out = append(out, bin...)

	path := filepath.Join(t.TempDir(), "fixture.glb")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func floatsToBytes(v []float32) []byte {
	out := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(f))
	}
	return out
}

func float32s(v []float64) []float32 {
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(f)
	}
	return out
}

func appendU32(b []byte, v uint32) []byte {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	return append(b, tmp[:]...)
}

// 节点变换要生效：单位立方体缩放 2 倍后包围盒翻倍。
func TestLoadGLBAppliesNodeTransform(t *testing.T) {
	path := buildGLB(t, glbFixture{
		Positions: [][3]float32{{0, 0, 0}, {1, 0, 0}, {0, 1, 1}},
		Scale:     [3]float64{2, 2, 2},
		Translate: [3]float64{1, 0, 0},
	})
	mesh, err := LoadGLB(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(mesh.Max.X-3) > 1e-5 {
		t.Fatalf("缩放/平移没生效: max.x=%.3f, want 3", mesh.Max.X)
	}
	if math.Abs(mesh.Max.Y-2) > 1e-5 {
		t.Fatalf("缩放没生效: max.y=%.3f, want 2", mesh.Max.Y)
	}
}

// 蒙皮网格按绑定姿势解算：关节平移 (0,0,5) + 权重 1 → 顶点整体平移 5。
func TestLoadGLBSkinnedBindPose(t *testing.T) {
	path := buildGLB(t, glbFixture{
		Positions:   [][3]float32{{0, 0, 0}, {1, 0, 0}},
		SkinJointAt: [3]float64{0, 0, 5},
	})
	mesh, err := LoadGLB(path)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(mesh.Min.Z-5) > 1e-5 || math.Abs(mesh.Max.Z-5) > 1e-5 {
		t.Fatalf("蒙皮绑定姿势没解算: z=[%.3f,%.3f], want 5", mesh.Min.Z, mesh.Max.Z)
	}
}

// 半径取"水平截面短边的一半"：长条模型（体长 2 格、躯干 0.4 格）半径应是 0.2，不含体长。
func TestDeriveUsesShortEdgeOfBand(t *testing.T) {
	mesh := &Mesh{
		Points: []Vec3{
			{X: -0.2, Y: 0.5, Z: -1}, {X: 0.2, Y: 0.5, Z: -1},
			{X: -0.2, Y: 0.5, Z: 1}, {X: 0.2, Y: 0.5, Z: 1},
			{X: -0.2, Y: 0.0, Z: 0}, {X: 0.2, Y: 1.0, Z: 0},
		},
		Min: Vec3{X: -0.2, Y: 0, Z: -1},
		Max: Vec3{X: 0.2, Y: 1, Z: 1},
	}
	proxy, err := Derive(mesh, Rules{Scale: 1, Kind: "capsule", Band: [2]float64{0, 1}, Percentile: 1})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(proxy.Radius-0.2) > 1e-3 {
		t.Fatalf("半径应为短边一半 0.2，实际 %.3f（extent %.3f×%.3f）", proxy.Radius, proxy.ExtentX, proxy.ExtentZ)
	}
	if math.Abs(proxy.Height-1) > 1e-3 {
		t.Fatalf("身高应为 1，实际 %.3f", proxy.Height)
	}
}

// 分位裁剪：少数离群点（飘带/粒子片）不能把半径撑大。
func TestDeriveTrimsOutliers(t *testing.T) {
	points := []Vec3{}
	for i := 0; i < 100; i++ {
		points = append(points, Vec3{X: -0.3, Y: 0.5, Z: -0.3}, Vec3{X: 0.3, Y: 0.5, Z: 0.3})
	}
	points = append(points, Vec3{X: -9, Y: 0.5, Z: 0}, Vec3{X: 9, Y: 0.5, Z: 0})
	mesh := &Mesh{Points: points, Min: Vec3{X: -9, Y: 0, Z: -0.3}, Max: Vec3{X: 9, Y: 1, Z: 0.3}}
	proxy, err := Derive(mesh, Rules{Scale: 1, Kind: "circle", Band: [2]float64{0, 1}, Percentile: 0.98})
	if err != nil {
		t.Fatal(err)
	}
	if proxy.Radius > 0.31 {
		t.Fatalf("离群点不该进半径：%.3f（max_radius=%.3f）", proxy.Radius, proxy.MaxRadius)
	}
	if proxy.MaxRadius < 8 {
		t.Fatalf("诊断用的 max_radius 应保留离群点，实际 %.3f", proxy.MaxRadius)
	}
}

// 取样高度带决定量哪一段：底部细、顶部粗的模型，取底部带得到细半径。
func TestDeriveBandSelectsHeight(t *testing.T) {
	mesh := &Mesh{
		Points: []Vec3{
			{X: -0.1, Y: 0.0, Z: -0.1}, {X: 0.1, Y: 0.0, Z: 0.1}, // 底部：细
			{X: -1.0, Y: 1.0, Z: -1.0}, {X: 1.0, Y: 1.0, Z: 1.0}, // 顶部：粗（树冠）
		},
		Min: Vec3{X: -1, Y: 0, Z: -1},
		Max: Vec3{X: 1, Y: 1, Z: 1},
	}
	bottom, err := Derive(mesh, Rules{Scale: 1, Kind: "circle", Band: [2]float64{0, 0.4}})
	if err != nil {
		t.Fatal(err)
	}
	top, err := Derive(mesh, Rules{Scale: 1, Kind: "circle", Band: [2]float64{0.6, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(bottom.Radius-0.1) > 1e-3 {
		t.Fatalf("底部带半径应为 0.1，实际 %.3f", bottom.Radius)
	}
	if math.Abs(top.Radius-1.0) > 1e-3 {
		t.Fatalf("顶部带半径应为 1.0，实际 %.3f", top.Radius)
	}
}

// 盒形足迹向上取整：1.4×0.92 格 → 2×1 占格。
func TestDeriveBoxTilesCeil(t *testing.T) {
	mesh := &Mesh{
		Points: []Vec3{{X: -0.7, Y: 0, Z: -0.46}, {X: 0.7, Y: 1, Z: 0.46}},
		Min:    Vec3{X: -0.7, Y: 0, Z: -0.46},
		Max:    Vec3{X: 0.7, Y: 1, Z: 0.46},
	}
	proxy, err := Derive(mesh, Rules{Scale: 1, Kind: "box", Band: [2]float64{0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	if proxy.BoxTiles != [2]int{2, 1} {
		t.Fatalf("占格应为 2×1，实际 %v（足迹 %.2f×%.2f）", proxy.BoxTiles, proxy.BoxFloat[0], proxy.BoxFloat[1])
	}
}
