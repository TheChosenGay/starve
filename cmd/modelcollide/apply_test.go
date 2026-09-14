package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"starve/pkg/modelcollide"
)

// 配置里"以名字为键的对象"（resource_templates）与"数组 + kind"（creatures/buildings）
// 两种形状都要能定位到字段，而且**不能被前面的字段带偏**。
// 这条锁的是曾经出现过的 bug：从对象开头找冒号 → valStart 指到前一个字段的值上，
// 于是把 "kind" 的字符串替换成了数字。
func TestFindValueSpanBothShapes(t *testing.T) {
	objectShape := []byte(`{
  "_doc": "x",
  "wood": {
    "name": "木头",
    "collision_radius": 0.103,
    "drop_table": [ { "kind": "wood", "count": 3 } ]
  }
}`)
	start, end, ok, err := findValueSpan(objectShape, "wood", "collision_radius")
	if err != nil || !ok {
		t.Fatalf("对象形状没找到字段: ok=%v err=%v", ok, err)
	}
	if got := string(objectShape[start:end]); got != "0.103" {
		t.Fatalf("区间内容 = %q, want 0.103", got)
	}

	arrayShape := []byte(`{
  "buildings": [
    {
      "kind": "campfire",
      "name": "火堆",
      "width": 2,
      "height": 2
    }
  ]
}`)
	// 最后一个字段：曾经的 bug 会把 valStart 指到 "campfire"
	start, end, ok, err = findValueSpan(arrayShape, "campfire", "height")
	if err != nil || !ok {
		t.Fatalf("数组形状没找到 height: ok=%v err=%v", ok, err)
	}
	if got := string(arrayShape[start:end]); got != "2" {
		t.Fatalf("height 区间 = %q, want 2（指错字段会变成 \"campfire\"）", got)
	}
	if s, _ := pickString(arrayShape, mustMembers(t, arrayShape, start), "kind"); s == "" {
		_ = s
	}

	// 每个字段都要指对
	for field, want := range map[string]string{"kind": `"campfire"`, "name": `"火堆"`, "width": "2", "height": "2"} {
		s, e, ok, err := findValueSpan(arrayShape, "campfire", field)
		if err != nil || !ok {
			t.Fatalf("%s 没找到: ok=%v err=%v", field, ok, err)
		}
		if got := string(arrayShape[s:e]); got != want {
			t.Fatalf("%s 区间 = %q, want %q", field, got, want)
		}
	}

	// 找不到就是不 ok（不报错）
	if _, _, ok, err := findValueSpan(arrayShape, "nope", "width"); ok || err != nil {
		t.Fatalf("不存在的条目应当 ok=false err=nil, got ok=%v err=%v", ok, err)
	}
	if _, _, ok, _ := findValueSpan(arrayShape, "campfire", "nope"); ok {
		t.Fatal("不存在的字段应当 ok=false")
	}
}

func mustMembers(t *testing.T, src []byte, _ int) []jsonMember {
	t.Helper()
	members, err := objectMembers(src, 0, len(src))
	if err != nil {
		t.Fatal(err)
	}
	return members
}

// -apply 必须**只改那个数字**：其它字节（含内联数组的排版）一个都不许动。
func TestApplyGrantsOnlyTouchesTargetNumber(t *testing.T) {
	dir := t.TempDir()
	original := `{
  "creatures": [
    { "kind": "rabbit", "drops": [ { "kind": "meat", "count": 1 } ], "body_radius": 0.107 },
    { "kind": "wolf", "drops": [ { "kind": "meat", "count": 2 } ], "body_radius": 0.246 }
  ]
}
`
	path := filepath.Join(dir, "creatures.json")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	grants := []Grant{{File: "creatures.json", Name: "wolf", Field: "body_radius", Value: 0.311}}

	// dry-run 不写盘
	changed, err := applyGrants(dir, grants, false)
	if err != nil || changed != 1 {
		t.Fatalf("dry-run changed=%d err=%v, want 1/nil", changed, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Fatal("dry-run 不应改文件")
	}

	// 落盘：只有 wolf 的 body_radius 变，排版与其它字段原样
	changed, err = applyGrants(dir, grants, true)
	if err != nil || changed != 1 {
		t.Fatalf("apply changed=%d err=%v", changed, err)
	}
	after, _ = os.ReadFile(path)
	want := strings.Replace(original, `"body_radius": 0.246`, `"body_radius": 0.311`, 1)
	if string(after) != want {
		t.Fatalf("只应替换一个数字：\n got: %s\nwant: %s", after, want)
	}

	// 已经一致 → 不再改动
	changed, err = applyGrants(dir, grants, true)
	if err != nil || changed != 0 {
		t.Fatalf("已一致时 changed=%d err=%v, want 0/nil", changed, err)
	}
}

// 字段不存在时明确失败，并且**不写坏文件**。
func TestApplyGrantsMissingFieldFails(t *testing.T) {
	dir := t.TempDir()
	original := `{ "creatures": [ { "kind": "wolf" } ] }` + "\n"
	path := filepath.Join(dir, "creatures.json")
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := applyGrants(dir, []Grant{{File: "creatures.json", Name: "wolf", Field: "body_radius", Value: 1}}, true)
	if err == nil {
		t.Fatal("缺字段应当报错（-apply 不新增字段）")
	}
	if !strings.Contains(err.Error(), "找不到") {
		t.Fatalf("错误信息应说明找不到字段, got %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Fatal("失败时不应改动文件")
	}
}

// 数值打印：整数不带小数点，小数保留原样。
func TestNumText(t *testing.T) {
	cases := map[float64]string{2: "2", 0.305: "0.305", 1000: "1000", 0.107: "0.107", 1.5: "1.5"}
	for in, want := range cases {
		if got := numText(in); got != want {
			t.Fatalf("numText(%v) = %q, want %q", in, got, want)
		}
	}
}

// ---- 验收规则 ----

func proxy(kind, axis, long string, outside float64) modelcollide.Proxy {
	p := modelcollide.Proxy{Kind: kind, OutsideRatio: outside, LongAxis: long}
	p.Capsule.Axis = axis
	p.Capsule.Radius = 0.2
	if axis == "body" {
		p.Capsule.A = [3]float64{0, 0.5, -1}
		p.Capsule.B = [3]float64{0, 0.5, 1}
	}
	return p
}

func TestAuditForwardAxis(t *testing.T) {
	// body 胶囊长轴在 X → 致命：客户端把局部 +Z 对准朝向，碰撞会差 90°
	var issues []issue
	auditProxy(Entry{Entity: "wolf", Shape: "capsule", CapsuleAxis: "body"}, proxy("capsule", "body", "x", 0.0), &issues)
	if len(issues) != 1 || !issues[0].Fatal {
		t.Fatalf("长轴在 X 应当报致命错误, got %+v", issues)
	}
	if !strings.Contains(issues[0].Msg, "90") {
		t.Fatalf("错误信息应点明差 90°, got %s", issues[0].Msg)
	}

	// 长轴在 Z → 通过
	issues = nil
	auditProxy(Entry{Entity: "wolf", Shape: "capsule", CapsuleAxis: "body"}, proxy("capsule", "body", "z", 0.02), &issues)
	if len(issues) != 0 {
		t.Fatalf("长轴在 Z 不应报问题, got %+v", issues)
	}
}

func TestAuditOutsideRatioOnlyForBodyCapsule(t *testing.T) {
	p := proxy("capsule", "body", "z", 0.30)
	var issues []issue
	auditProxy(Entry{Entity: "wolf", Shape: "capsule", CapsuleAxis: "body"}, p, &issues)
	if len(issues) != 1 || issues[0].Fatal {
		t.Fatalf("包含率超限应当是警告, got %+v", issues)
	}
	// 逐条覆盖阈值
	high := 0.5
	issues = nil
	auditProxy(Entry{Entity: "wolf", Shape: "capsule", CapsuleAxis: "body", MaxOutside: &high}, p, &issues)
	if len(issues) != 0 {
		t.Fatalf("覆盖阈值后不应报警, got %+v", issues)
	}
	// 直立胶囊（人形）不卡包含率：手臂本来就在半径外
	issues = nil
	auditProxy(Entry{Entity: "player", Shape: "capsule", CapsuleAxis: "vertical"}, proxy("capsule", "vertical", "", 0.87), &issues)
	if len(issues) != 0 {
		t.Fatalf("直立胶囊不应因包含率报警（实测玩家 87%%）, got %+v", issues)
	}
}

func TestAuditGroundOffsetOnlyForNonBox(t *testing.T) {
	p := proxy("capsule", "vertical", "", 0.0)
	p.Bounds = [3][2]float64{{-1, 1}, {-0.95, 0.95}, {-0.5, 0.5}}
	var issues []issue
	auditProxy(Entry{Entity: "workbench", Shape: "capsule"}, p, &issues)
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "离地") {
		t.Fatalf("非 box 形状原点离地应报警, got %+v", issues)
	}
	issues = nil
	auditProxy(Entry{Entity: "workbench", Shape: "box"}, p, &issues)
	if len(issues) != 0 {
		t.Fatalf("box 形状不受 Y 影响, 不该报警, got %+v", issues)
	}
}

func TestAuditCenterOffset(t *testing.T) {
	p := proxy("circle", "vertical", "", 0.0)
	p.CenterOffset = [2]float64{0.25, 0.0}
	var issues []issue
	auditProxy(Entry{Entity: "tree", Shape: "circle"}, p, &issues)
	if len(issues) != 1 || !strings.Contains(issues[0].Msg, "形心") {
		t.Fatalf("形心偏太多应报警, got %+v", issues)
	}
	limit := 0.5
	issues = nil
	auditProxy(Entry{Entity: "tree", Shape: "circle", MaxCenterOffset: &limit}, p, &issues)
	if len(issues) != 0 {
		t.Fatalf("覆盖阈值后不应报警, got %+v", issues)
	}
}

// scan_ignore 支持普通 glob 与 "dir/**" 前缀两种写法。
func TestMatchAnyAndScanAssets(t *testing.T) {
	if !matchAny([]string{"models/pigman/mixamo/*.glb"}, "models/pigman/mixamo/death.glb") {
		t.Fatal("glob 应命中")
	}
	if matchAny([]string{"models/pigman/mixamo/*.glb"}, "models/pigman/walk.glb") {
		t.Fatal("glob 不应命中其它目录")
	}
	if !matchAny([]string{"models/ual/**"}, "models/ual/rig/foo.glb") {
		t.Fatal("dir/** 前缀写法应命中")
	}

	dir := t.TempDir()
	for _, rel := range []string{"models/a/one.glb", "models/a/two.glb", "models/b/three.glb"} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := Manifest{
		Models:     []Entry{{Entity: "one", Model: "models/a/one.glb"}},
		Scan:       []string{"models/*/*.glb"},
		ScanIgnore: []string{"models/b/*.glb"},
	}
	got, err := scanAssets(m, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "models/a/two.glb" {
		t.Fatalf("未登记模型 = %v, want [models/a/two.glb]", got)
	}
}
