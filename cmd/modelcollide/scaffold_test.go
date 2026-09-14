package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"starve/pkg/modelcollide"
)

// fakeMesh 造一个 2×1×4（长轴 Z）的"四足"顶点云：够 Derive 算出截面。
func fakeMesh() *modelcollide.Mesh {
	m := &modelcollide.Mesh{
		Min: modelcollide.Vec3{X: -1, Y: 0, Z: -2},
		Max: modelcollide.Vec3{X: 1, Y: 1, Z: 2},
	}
	for _, y := range []float64{0, 0.25, 0.5, 0.75, 1} {
		m.Points = append(m.Points,
			modelcollide.Vec3{X: -1, Y: y, Z: -2},
			modelcollide.Vec3{X: 1, Y: y, Z: 2},
			modelcollide.Vec3{X: -1, Y: y, Z: 2},
			modelcollide.Vec3{X: 1, Y: y, Z: -2},
		)
	}
	return m
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Alpaca":             "alpaca",
		"White Horse":        "white_horse",
		"Shiba Inu":          "shiba_inu",
		"ghibli_tree_godot":  "ghibli_tree_godot",
		"alchemy-engine":     "alchemy_engine",
		"  weird--name__x  ": "weird_name_x",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Fatalf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// 几何兜底：长条→四足胶囊；高瘦→直立胶囊；矮方→盒；其余→格心圆。
func TestGuessByGeometry(t *testing.T) {
	cases := []struct {
		name                string
		dx, dy, dz          float64
		wantShape, wantAxis string
	}{
		{"长条（四足）", 1, 1, 4, "capsule", "body"},
		{"高瘦（人形）", 0.5, 3, 0.5, "capsule", "vertical"},
		{"矮而方正（建筑）", 3, 1, 3, "box", ""},
		{"矮而略长（建筑）", 1, 1, 1.2, "box", ""},
		{"立方体（树/石这类格心圆）", 1, 1, 1, "circle", ""},
	}
	for _, c := range cases {
		mesh := &modelcollide.Mesh{
			Min: modelcollide.Vec3{},
			Max: modelcollide.Vec3{X: c.dx, Y: c.dy, Z: c.dz},
		}
		shape, axis, _ := guessByGeometry(mesh)
		if shape != c.wantShape || axis != c.wantAxis {
			t.Fatalf("%s: got %s/%s, want %s/%s", c.name, shape, axis, c.wantShape, c.wantAxis)
		}
	}
}

// 同目录条目：形状取多数、scale 取中位数。
func TestPickSiblingAndMedianScale(t *testing.T) {
	sibs := []Entry{
		{Shape: "capsule", CapsuleAxis: "body", Scale: 0.22},
		{Shape: "capsule", CapsuleAxis: "body", Scale: 0.48},
		{Shape: "circle", Scale: 0.40},
	}
	if got := pickSibling(sibs); got.Shape != "capsule" {
		t.Fatalf("应当选多数的 capsule, got %s", got.Shape)
	}
	if got := medianScale(sibs); got != 0.40 {
		t.Fatalf("scale 中位数 = %v, want 0.40", got)
	}
	if got := medianScale(nil); got != 1 {
		t.Fatalf("没有参考时应回落 1, got %v", got)
	}
}

// 往手写清单里插条目：**只新增字节**，原有内容的排版一个字节都不能动。
func TestInsertManifestEntriesPreservesFormatting(t *testing.T) {
	root := t.TempDir()
	original := `{
  "_doc": [
    "说明"
  ],
  "models": [
    {
      "entity": "wood",
      "model": "models/tree.glb",
      "scale": 0.56,
      "targets": [
        {
          "kind": "resource_template",
          "field": "collision_radius"
        }
      ]
    }
  ],
  "asset_repo": "x"
}
`
	writeFile(t, filepath.Join(root, manifestPath), original)
	n, err := insertManifestEntries(root, []Entry{{Entity: "alpaca", Model: "models/a.glb", Scale: 0.4, Shape: "capsule"}})
	if err != nil || n != 1 {
		t.Fatalf("插入失败 n=%d err=%v", n, err)
	}
	got, _ := os.ReadFile(filepath.Join(root, manifestPath))

	// 原有内容必须**逐行原样保留**，只允许新增行（以及给最后一条补一个逗号）。
	// 做法：把 got 的每行与 original 逐行做子序列匹配，匹配不上就算"新增行"。
	norm := func(s string) string { return strings.TrimRight(strings.TrimSpace(s), ",") }
	origLines := strings.Split(original, "\n")
	gotLines := strings.Split(string(got), "\n")
	i := 0
	for _, l := range gotLines {
		if i < len(origLines) && l == origLines[i] {
			i++
			continue
		}
		if i < len(origLines) && norm(l) == norm(origLines[i]) {
			i++ // 只多了个逗号
			continue
		}
	}
	if i != len(origLines) {
		t.Fatalf("原有行没有被原样保留（匹配到 %d/%d 行）：\n%s", i, len(origLines), got)
	}
	// 仍然是合法 JSON，且两条都在
	var doc struct {
		Models []Entry `json:"models"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("插入后不是合法 JSON: %v", err)
	}
	if len(doc.Models) != 2 || doc.Models[1].Entity != "alpaca" {
		t.Fatalf("应当有 2 条且第二条是 alpaca, got %+v", doc.Models)
	}
}

// 空数组也要能插对（缩进从数组那一行推）。
func TestInsertManifestEntriesEmptyArray(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, manifestPath), `{
  "models": []
}
`)
	if _, err := insertManifestEntries(root, []Entry{{Entity: "a"}, {Entity: "b"}}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(root, manifestPath))
	var doc struct {
		Models []Entry `json:"models"`
	}
	if err := json.Unmarshal(got, &doc); err != nil {
		t.Fatalf("不是合法 JSON: %v\n%s", err, got)
	}
	if len(doc.Models) != 2 {
		t.Fatalf("应当插入 2 条, got %d\n%s", len(doc.Models), got)
	}
	if !strings.Contains(string(got), "\"entity\": \"a\"") {
		t.Fatalf("缩进/内容不对:\n%s", got)
	}
}

// 同目录有参考时：shape/band/targets 抄同目录，scale 取中位数并标 TODO；
// 目标配置里没有的条目 → **省略 targets**（否则 -apply 会失败、CI 红）。
func TestBuildCandidateCopiesSiblingAndDropsMissingTargets(t *testing.T) {
	m := Manifest{Models: []Entry{
		{
			Entity: "wolf", Model: "models/animals/Wolf.glb", Scale: 0.48,
			Shape: "capsule", CapsuleAxis: "body", Band: [2]float64{0.25, 0.75},
			Percentile: 0.98,
			Targets: []Target{
				{Kind: "creature", Field: "body_radius"},
				{Kind: "creature", Field: "body_height"},
			},
		},
	}}
	entry, summary, notes := buildCandidate("models/animals/Alpaca.glb", fakeMesh(), m,
		func(kind, name string) bool { return false }) // 配置里还没有 alpaca

	if entry.Entity != "alpaca" {
		t.Fatalf("entity = %q, want alpaca", entry.Entity)
	}
	if entry.Shape != "capsule" || entry.CapsuleAxis != "body" {
		t.Fatalf("应当抄同目录的 capsule/body, got %s/%s", entry.Shape, entry.CapsuleAxis)
	}
	if entry.Band != [2]float64{0.25, 0.75} {
		t.Fatalf("band 应当抄同目录, got %v", entry.Band)
	}
	if entry.Scale != 0.48 {
		t.Fatalf("scale 应当取同目录中位数 0.48, got %v", entry.Scale)
	}
	if !strings.Contains(entry.ScaleSource, "TODO") {
		t.Fatalf("scale_source 必须留 TODO（猜不出客户端 ModelScale）: %q", entry.ScaleSource)
	}
	if len(entry.Targets) != 0 {
		t.Fatalf("配置里没有该条目时 targets 必须省略, got %+v", entry.Targets)
	}
	if !strings.Contains(entry.Note, "targets 暂时省略") {
		t.Fatalf("note 要说明为什么省略 targets: %q", entry.Note)
	}
	if len(notes) == 0 || !strings.Contains(summary, "推导") {
		t.Fatalf("应当给出推导摘要与提示: summary=%q notes=%v", summary, notes)
	}
	if !strings.Contains(entry.Note, "scale 与 band 必须人工确认") {
		t.Fatalf("note 必须点出要人工确认的字段: %q", entry.Note)
	}
}

// 目标配置里已经有该条目时，targets 要保留（这样 -apply 能直接落字段）。
func TestBuildCandidateKeepsTargetsWhenConfigExists(t *testing.T) {
	m := Manifest{Models: []Entry{{
		Entity: "wolf", Model: "models/animals/Wolf.glb", Scale: 0.48,
		Shape: "capsule", CapsuleAxis: "body", Band: [2]float64{0.25, 0.75},
		Targets: []Target{{Kind: "creature", Field: "body_radius"}},
	}}}
	entry, _, _ := buildCandidate("models/animals/Husky.glb", fakeMesh(), m,
		func(kind, name string) bool { return kind == "creature" && name == "husky" })
	if len(entry.Targets) != 1 {
		t.Fatalf("配置已有该条目时应当保留 targets, got %+v", entry.Targets)
	}
	// 名字要补成实体名（清单里的空 name 表示"用 entry.Entity"）
	if entry.Targets[0].Name != "husky" {
		t.Fatalf("targets.name 应当补成实体名, got %q", entry.Targets[0].Name)
	}
}

// 长轴不在 Z 的四足：必须给出"碰撞会差 90°"的硬提示。
func TestBuildCandidateWarnsOnWrongLongAxis(t *testing.T) {
	// 造一个长轴在 X 的模型
	mesh := &modelcollide.Mesh{
		Min: modelcollide.Vec3{X: -2, Y: 0, Z: -1},
		Max: modelcollide.Vec3{X: 2, Y: 1, Z: 1},
	}
	for _, y := range []float64{0, 0.5, 1} {
		mesh.Points = append(mesh.Points,
			modelcollide.Vec3{X: -2, Y: y, Z: -1},
			modelcollide.Vec3{X: 2, Y: y, Z: 1},
		)
	}
	m := Manifest{Models: []Entry{{
		Entity: "wolf", Model: "models/animals/Wolf.glb", Scale: 0.48,
		Shape: "capsule", CapsuleAxis: "body", Band: [2]float64{0.25, 0.75},
	}}}
	_, _, notes := buildCandidate("models/animals/Sideways.glb", mesh, m, func(string, string) bool { return false })
	found := false
	for _, n := range notes {
		if strings.Contains(n, "90") {
			found = true
		}
	}
	if !found {
		t.Fatalf("长轴在 X 时必须提示差 90°, got %v", notes)
	}
}
