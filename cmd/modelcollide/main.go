// Command modelcollide 是"客户端模型 → 服务端简化碰撞体"的流水线。
//
//	# 查看当前所有模型推导出来的碰撞体（只读）
//	go run ./cmd/modelcollide
//
//	# 重新生成 configs/model_collision.json（改了模型或清单后）
//	go run ./cmd/modelcollide -write
//
//	# 校验：生成文件与游戏配置是否一致（不需要模型文件，可进 CI）
//	go run ./cmd/modelcollide -check
//
//	# 校验：重新读模型推导，与生成文件逐字段对比（需要模型文件；抓"改了模型忘了跑流水线"）
//	go run ./cmd/modelcollide -verify
//
// 清单 configs/models.json 手工维护：每个模型挂到哪个实体、客户端缩放常量是多少、
// 取哪一段高度、百分位多少、推导出来的值落到哪个配置字段。
//
// 输出原则：本工具**不直接改**手写配置（resource_templates/buildings/creatures），
// 只生成 configs/model_collision.json 并校验手写配置是否与之一致；
// 不一致时打印"该改成多少"，由人确认后改配置。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"starve/pkg/modelcollide"
)

const (
	manifestPath = "configs/models.json"
	outputPath   = "configs/model_collision.json"
)

// Manifest 是模型清单（手工维护）。
type Manifest struct {
	// AssetRoot 客户端资产根目录（相对仓库根；可用 -asset-root 覆盖）。
	AssetRoot string  `json:"asset_root"`
	Models    []Entry `json:"models"`
	// Scan 是"扫描漏登记模型"的 glob（相对 asset_root）：命中但没被 models 引用的
	// .glb 会作为线索列出来——新加的动物/资源模型往往就是这种情况。
	Scan []string `json:"scan"`
	// ScanIgnore 豁免 scan（动画片段、同款贴图变体等合法未引用）。
	ScanIgnore []string `json:"scan_ignore"`
}

// Entry 一条"实体 ↔ 模型"映射。
type Entry struct {
	Entity      string     `json:"entity"`       // 服务端实体/配置名（wood / wolf / campfire …）
	Model       string     `json:"model"`        // 相对 asset_root 的模型路径；空 = 不用模型（见 Fixed）
	Scale       float64    `json:"scale"`        // 模型单位 → 格（= 客户端 ModelScale）
	ScaleSource string     `json:"scale_source"` // 缩放常量出处（客户端文件），便于对齐
	Shape       string     `json:"shape"`        // circle / capsule / box
	Band        [2]float64 `json:"band"`         // 取样高度带（0..1；缺省全高）
	Percentile  float64    `json:"percentile"`   // 半径百分位（缺省 0.98）
	CapsuleAxis string     `json:"capsule_axis"` // vertical（缺省）| body（四足：段沿水平长轴）
	// MaxOutside/MaxCenterOffset 逐条覆盖验收阈值（缺省见 audit.go 的 default*）。
	MaxOutside      *float64 `json:"max_outside"`
	MaxCenterOffset *float64 `json:"max_center_offset"`
	Fixed           *Fixed   `json:"fixed"`   // 没有可量模型时手工给值（必须写原因）
	Targets         []Target `json:"targets"` // 推导值落到哪些配置字段
	Note            string   `json:"note"`    // 需要人知道的坑（会原样进生成文件）
}

// Fixed 手工值（2D 精灵、客户端基本体、纯玩法尺寸）。
type Fixed struct {
	Radius   float64 `json:"radius,omitempty"`
	Height   float64 `json:"height,omitempty"`
	BoxTiles [2]int  `json:"box_tiles,omitempty"`
	Reason   string  `json:"reason"`
}

// Target 一条配置落点。Mode: exact（必须等于）| at_least（必须 ≥，用于占格盒覆盖模型足迹）。
type Target struct {
	Kind  string `json:"kind"`  // resource_template | building | creature
	Name  string `json:"name"`  // 配置里的名字；空 = Entry.Entity
	Field string `json:"field"` // 字段名
	Mode  string `json:"mode"`  // exact（缺省）| at_least
}

// Output 是生成文件结构（确定性排序）。
type Output struct {
	Generator string   `json:"generator"`
	Proxies   []Record `json:"proxies"`
}

// Record 一个实体的简化碰撞体 + 它要求配置里是什么值。
type Record struct {
	Entity      string  `json:"entity"`
	Model       string  `json:"model,omitempty"`
	Scale       float64 `json:"scale,omitempty"`
	ScaleSource string  `json:"scale_source,omitempty"`
	// ModelSHA256 是模型文件内容摘要：用来区分"模型变了"和"推导规则变了"，
	// 也让 -check（不读模型）至少有据可查。
	ModelSHA256 string              `json:"model_sha256,omitempty"`
	Shape       string              `json:"shape"`
	Proxy       *modelcollide.Proxy `json:"proxy,omitempty"`
	Fixed       *Fixed              `json:"fixed,omitempty"`
	Grants      []Grant             `json:"grants,omitempty"`
	Note        string              `json:"note,omitempty"`
}

// Grant 是"配置里这个字段应该等于/至少等于这个值"。
type Grant struct {
	File  string  `json:"file"`
	Name  string  `json:"name"`
	Field string  `json:"field"`
	Mode  string  `json:"mode"`
	Value float64 `json:"value"`
}

func main() {
	var (
		write     = flag.Bool("write", false, "重新生成 "+outputPath)
		check     = flag.Bool("check", false, "校验生成文件与游戏配置一致（不需要模型）")
		verify    = flag.Bool("verify", false, "重新读模型推导并与生成文件对比（需要模型）")
		assetRoot = flag.String("asset-root", "", "覆盖清单里的 asset_root（也可用 GATE_ASSET_ROOT）")
		verbose   = flag.Bool("v", false, "打印每条推导的明细")
		repoRoot  = flag.String("root", ".", "仓库根目录")
		apply     = flag.Bool("apply", false, "把推导值写进手写配置（默认 dry-run，加 -yes 才落盘）")
		yes       = flag.Bool("yes", false, "配合 -apply：真的写入文件")
		strict    = flag.Bool("strict", false, "把验收警告升级为失败（CI 用）")
	)
	flag.Parse()

	if err := run(options{
		root: *repoRoot, assetRoot: *assetRoot, write: *write, check: *check,
		verify: *verify, verbose: *verbose, apply: *apply, yes: *yes, strict: *strict,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "modelcollide:", err)
		os.Exit(1)
	}
}

// options 是 run 的入参（开关多了以后用结构体，避免一长串 bool）。
type options struct {
	root, assetRoot string
	write, check    bool
	verify, verbose bool
	apply, yes      bool
	strict          bool
}

func run(o options) error {
	root, write, check, verify, verbose := o.root, o.write, o.check, o.verify, o.verbose
	manifest, err := loadManifest(filepath.Join(root, manifestPath))
	if err != nil {
		return err
	}
	if o.assetRoot != "" {
		manifest.AssetRoot = o.assetRoot
	} else if env := os.Getenv("GATE_ASSET_ROOT"); env != "" {
		// CI 里常常把资源仓 checkout 到别处：用环境变量指过去，不改清单。
		manifest.AssetRoot = env
	}
	assetRoot := manifest.AssetRoot
	if !filepath.IsAbs(assetRoot) {
		assetRoot = filepath.Join(root, assetRoot)
	}

	// -check 只需要生成文件 + 游戏配置（可进 CI，不需要客户端模型）；
	// 报表 / -write / -verify / 验收 都要读模型。
	var output Output
	var issues []issue
	if check && !write && !verify {
		output, err = loadOutput(filepath.Join(root, outputPath))
		if err != nil {
			return err
		}
	} else {
		records, err := deriveAll(manifest, assetRoot, verbose, &issues)
		if err != nil {
			return err
		}
		output = Output{Generator: "cmd/modelcollide", Proxies: records}
	}

	// 自动验收：朝向/贴合/原点/居中（见 audit.go）。
	fatal := printIssues(issues)
	if o.strict && len(issues) > fatal {
		fatal = len(issues)
	}
	if fatal > 0 {
		return fmt.Errorf("验收未通过：%d 项（见上面的错误；警告共 %d 项）", fatal, len(issues))
	}

	// 漏登记扫描：新加的模型没写进清单时给线索。
	if !(check && !write && !verify) && len(manifest.Scan) > 0 {
		unregistered, err := scanAssets(manifest, assetRoot, verbose)
		if err != nil {
			return err
		}
		if len(unregistered) > 0 {
			fmt.Printf("\n未登记模型（%d）：这些 .glb 没被清单引用——新资源/新生物？\n", len(unregistered))
			for _, u := range unregistered {
				fmt.Printf("  %s\n", u)
			}
			fmt.Println("  要接入就加 models 条目；确认不需要就加进 scan_ignore。" +
				"（这条只是线索，不会让流水线失败）")
		}
	}

	if write {
		if err := writeOutput(filepath.Join(root, outputPath), output); err != nil {
			return err
		}
		fmt.Printf("已写出 %s（%d 条）\n", outputPath, len(output.Proxies))
	}
	if !check && !verify {
		printReport(output.Proxies)
	}

	if verify {
		if err := verifyAgainstFile(filepath.Join(root, outputPath), output); err != nil {
			return err
		}
		fmt.Printf("模型重算与 %s 一致（%d 条）\n", outputPath, len(output.Proxies))
	}
	if check || verify {
		if err := checkGrants(root, output); err != nil {
			return err
		}
		fmt.Println("配置字段与流水线一致")
	}

	// -apply：把手写配置里的字段同步成推导值（默认 dry-run）。
	if o.apply {
		grants := collectGrants(output)
		fmt.Printf("\n同步手写配置（%d 条落点）：\n", len(grants))
		changed, err := applyGrants(root, grants, o.yes)
		if err != nil {
			return err
		}
		if changed == 0 {
			fmt.Println("  已经一致，无需改动")
		} else if o.yes {
			fmt.Printf("已写入 %d 处\n", changed)
		} else {
			fmt.Printf("dry-run：共需改 %d 处；确认后加 -yes 落盘\n", changed)
		}
	}
	return nil
}

// collectGrants 汇总所有记录的落点。
func collectGrants(output Output) []Grant {
	var out []Grant
	for _, r := range output.Proxies {
		out = append(out, r.Grants...)
	}
	return out
}

// printIssues 打印验收发现，返回 fatal 数量。
func printIssues(issues []issue) int {
	if len(issues) == 0 {
		return 0
	}
	fatal := 0
	fmt.Println("\n自动验收：")
	for _, it := range issues {
		if it.Fatal {
			fatal++
		}
		fmt.Printf("  %s\n", it)
	}
	return fatal
}

// loadOutput 读取已生成的流水线输出（-check 用，不需要模型）。
func loadOutput(path string) (Output, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Output{}, fmt.Errorf("读不到 %s（先跑 go run ./cmd/modelcollide -write）: %w", path, err)
	}
	var out Output
	if err := json.Unmarshal(raw, &out); err != nil {
		return Output{}, fmt.Errorf("%s 解析失败: %w", path, err)
	}
	if len(out.Proxies) == 0 {
		return Output{}, fmt.Errorf("%s 里没有 proxies", path)
	}
	return out, nil
}

// deriveAll 逐条推导（顺序固定：清单顺序，输出再按实体名排序）。
func deriveAll(m Manifest, assetRoot string, verbose bool, issues *[]issue) ([]Record, error) {
	if len(m.Models) == 0 {
		return nil, fmt.Errorf("%s 里没有 models", manifestPath)
	}
	seen := map[string]bool{}
	records := make([]Record, 0, len(m.Models))
	for _, e := range m.Models {
		if e.Entity == "" {
			return nil, fmt.Errorf("清单里有条目缺 entity")
		}
		if seen[e.Entity] {
			return nil, fmt.Errorf("清单里 entity %q 重复", e.Entity)
		}
		seen[e.Entity] = true

		rec := Record{
			Entity:      e.Entity,
			Model:       e.Model,
			Scale:       e.Scale,
			ScaleSource: e.ScaleSource,
			Shape:       e.Shape,
			Fixed:       e.Fixed,
			Note:        e.Note,
		}
		if e.Model != "" {
			path := filepath.Join(assetRoot, filepath.FromSlash(e.Model))
			sum, err := hashFile(path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Entity, err)
			}
			rec.ModelSHA256 = sum
			mesh, err := modelcollide.LoadGLB(path)
			if err != nil {
				return nil, err
			}
			proxy, err := modelcollide.Derive(mesh, modelcollide.Rules{
				Scale:      e.Scale,
				Kind:       e.Shape,
				Band:       e.Band,
				Percentile: e.Percentile,
				Axis:       e.CapsuleAxis,
			})
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Entity, err)
			}
			rec.Proxy = &proxy
			auditProxy(e, proxy, issues)
			if verbose {
				fmt.Printf("  %-9s %-42s 顶点 %6d 半径 %.3f 高 %.3f 足迹 %.2f×%.2f 长轴 %-4s 包含率 %.3f 离地 %+.3f\n",
					e.Entity, e.Model, proxy.Points, proxy.Radius, proxy.Height,
					proxy.BoxFloat[0], proxy.BoxFloat[1], longAxisLabel(proxy.LongAxis),
					proxy.OutsideRatio, proxy.Bounds[1][0])
			}
		} else if e.Fixed == nil {
			return nil, fmt.Errorf("%s: 既没有 model 也没有 fixed（无模型必须写明原因）", e.Entity)
		}
		grants, err := grantsFor(e, rec)
		if err != nil {
			return nil, err
		}
		rec.Grants = grants
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Entity < records[j].Entity })
	return records, nil
}

// grantsFor 把推导结果映射成"配置里该是什么值"。
func grantsFor(e Entry, rec Record) ([]Grant, error) {
	if len(e.Targets) == 0 {
		return nil, nil
	}
	value := func(field string) (float64, error) {
		switch {
		case rec.Proxy != nil:
			switch field {
			case "collision_radius", "body_radius":
				return rec.Proxy.Radius, nil
			case "body_height":
				return rec.Proxy.Height, nil
			case "body_half_length":
				return rec.Proxy.Capsule.HalfLength(), nil
			case "width", "height":
				idx := 0
				if field == "height" {
					idx = 1
				}
				return float64(rec.Proxy.BoxTiles[idx]), nil
			}
		case rec.Fixed != nil:
			switch field {
			case "collision_radius", "body_radius":
				return rec.Fixed.Radius, nil
			case "body_height":
				return rec.Fixed.Height, nil
			case "body_half_length":
				return 0, nil // fixed 形状（2D 精灵等）按直立圆柱处理
			case "width":
				return float64(rec.Fixed.BoxTiles[0]), nil
			case "height":
				return float64(rec.Fixed.BoxTiles[1]), nil
			}
		}
		return 0, fmt.Errorf("%s: 形状 %q 不支持字段 %q", e.Entity, rec.Shape, field)
	}

	out := make([]Grant, 0, len(e.Targets))
	for _, t := range e.Targets {
		name := t.Name
		if name == "" {
			name = e.Entity
		}
		v, err := value(t.Field)
		if err != nil {
			return nil, err
		}
		mode := t.Mode
		if mode == "" {
			mode = "exact"
		}
		out = append(out, Grant{
			File:  configFileOf(t.Kind),
			Name:  name,
			Field: t.Field,
			Mode:  mode,
			Value: v,
		})
	}
	return out, nil
}

// longAxisLabel 长轴显示：直立形状没有水平长轴。
func longAxisLabel(axis string) string {
	if axis == "" {
		return "直立"
	}
	return axis
}

func configFileOf(kind string) string {
	switch kind {
	case "resource_template":
		return "configs/resource_templates.json"
	case "building":
		return "configs/buildings.json"
	case "creature":
		return "configs/creatures.json"
	}
	return kind
}

// writeOutput 确定性写出生成文件。
func writeOutput(path string, output Output) error {
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// printReport 打印人可读的推导结果。
func printReport(records []Record) {
	fmt.Printf("%-9s %-9s %-8s %-8s %-6s %-7s %-7s %s\n", "实体", "形状", "半径", "身高", "占格", "包含率", "离地", "来源")
	for _, r := range records {
		radius, height, tiles := "-", "-", "-"
		source := "（无来源）"
		if r.Fixed != nil {
			source = "固定：" + r.Fixed.Reason
			if r.Shape == "circle" || r.Shape == "capsule" {
				radius = fmt.Sprintf("%.3f", r.Fixed.Radius)
			}
			if r.Shape == "capsule" {
				height = fmt.Sprintf("%.3f", r.Fixed.Height)
			}
			if r.Shape == "box" {
				tiles = fmt.Sprintf("%d×%d", r.Fixed.BoxTiles[0], r.Fixed.BoxTiles[1])
			}
		}
		if r.Proxy != nil {
			source = r.Model
			switch r.Shape {
			case "circle":
				radius = fmt.Sprintf("%.3f", r.Proxy.Radius)
			case "capsule":
				radius = fmt.Sprintf("%.3f", r.Proxy.Radius)
				height = fmt.Sprintf("%.3f", r.Proxy.Height)
			case "box":
				tiles = fmt.Sprintf("%d×%d", r.Proxy.BoxTiles[0], r.Proxy.BoxTiles[1])
			}
			if off := r.Proxy.CenterOffset; off[0] != 0 || off[1] != 0 {
				source += fmt.Sprintf("（形心偏差 %.3f,%.3f）", off[0], off[1])
			}
		}
		ratio, ground := "-", "-"
		if r.Proxy != nil {
			ratio = fmt.Sprintf("%.3f", r.Proxy.OutsideRatio)
			ground = fmt.Sprintf("%+.3f", r.Proxy.Bounds[1][0])
		}
		fmt.Printf("%-9s %-9s %-8s %-8s %-6s %-7s %-7s %s\n", r.Entity, r.Shape, radius, height, tiles, ratio, ground, source)
	}
}

// verifyAgainstFile 重新推导并与生成文件逐字段对比。
func verifyAgainstFile(path string, want Output) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读不到 %s（先跑 -write）: %w", path, err)
	}
	var have Output
	if err := json.Unmarshal(raw, &have); err != nil {
		return fmt.Errorf("%s 解析失败: %w", path, err)
	}
	haveByEntity := map[string]Record{}
	for _, r := range have.Proxies {
		haveByEntity[r.Entity] = r
	}
	problems := 0
	for _, wantRec := range want.Proxies {
		old, ok := haveByEntity[wantRec.Entity]
		if !ok {
			fmt.Printf("差异 %s: 生成文件里没有这条（跑 -write）\n", wantRec.Entity)
			problems++
			continue
		}
		if diff := diffRecord(old, wantRec); diff != "" {
			fmt.Printf("差异 %s: %s\n", wantRec.Entity, diff)
			problems++
		}
	}
	if problems > 0 {
		return fmt.Errorf("模型与 %s 不一致（%d 处）；跑 go run ./cmd/modelcollide -write 重新生成", path, problems)
	}
	return nil
}

func diffRecord(old, want Record) string {
	var diffs []string
	switch {
	case old.Proxy == nil && want.Proxy == nil:
		if old.Fixed == nil || want.Fixed == nil {
			return ""
		}
		if old.Fixed.Radius != want.Fixed.Radius || old.Fixed.Height != want.Fixed.Height ||
			old.Fixed.BoxTiles != want.Fixed.BoxTiles {
			diffs = append(diffs, "fixed 值变了")
		}
	case old.Proxy == nil || want.Proxy == nil:
		diffs = append(diffs, "形状来源变了（proxy/fixed 互换）")
	default:
		if old.Proxy.Radius != want.Proxy.Radius {
			diffs = append(diffs, fmt.Sprintf("radius %.3f → %.3f", old.Proxy.Radius, want.Proxy.Radius))
		}
		if old.Proxy.Height != want.Proxy.Height {
			diffs = append(diffs, fmt.Sprintf("height %.3f → %.3f", old.Proxy.Height, want.Proxy.Height))
		}
		if old.Proxy.BoxTiles != want.Proxy.BoxTiles {
			diffs = append(diffs, fmt.Sprintf("box_tiles %v → %v", old.Proxy.BoxTiles, want.Proxy.BoxTiles))
		}
		if old.Scale != want.Scale || old.Model != want.Model {
			diffs = append(diffs, "模型或缩放变了")
		}
	}
	return strings.Join(diffs, "; ")
}

// checkGrants 校验游戏配置里的字段与流水线要求一致。
func checkGrants(root string, output Output) error {
	problems := 0
	for _, rec := range output.Proxies {
		for _, g := range rec.Grants {
			got, err := readConfigNumber(root, g.File, g.Name, g.Field)
			if err != nil {
				fmt.Printf("缺失 %s: %s.%s —— %v（应为 %.3f）\n", g.File, g.Name, g.Field, err, g.Value)
				problems++
				continue
			}
			ok := false
			switch g.Mode {
			case "at_least":
				ok = got >= g.Value-1e-9
			default:
				ok = abs(got-g.Value) <= 1e-6
			}
			if !ok {
				rel := "="
				if g.Mode == "at_least" {
					rel = "≥"
				}
				fmt.Printf("不一致 %s: %s.%s 当前 %.3f，应 %s %.3f\n",
					g.File, g.Name, g.Field, got, rel, g.Value)
				problems++
			}
		}
	}
	if problems > 0 {
		return fmt.Errorf("有 %d 处配置与流水线推导不一致（改上面列出的字段，或修 configs/models.json 的规则）", problems)
	}
	return nil
}

// readConfigNumber 读配置里某个命名条目的数值字段。
// 支持两类容器：根对象（键=名字，如 resource_templates.json）与
// {"<容器名>": [...]}（数组里按 kind 找，如 buildings.json / creatures.json）。
func readConfigNumber(root, file, name, field string) (float64, error) {
	raw, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return 0, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return 0, fmt.Errorf("解析失败: %w", err)
	}
	// 根对象形态：{"<name>": {..., "<field>": n}}
	if obj, ok := doc[name]; ok {
		if v, ok := numberField(obj, field); ok {
			return v, nil
		}
	}
	// 数组形态：{"buildings": [{"kind": "...", ...}]}
	for _, listRaw := range doc {
		var list []map[string]json.RawMessage
		if err := json.Unmarshal(listRaw, &list); err != nil {
			continue
		}
		for _, item := range list {
			kindRaw, ok := item["kind"]
			if !ok {
				continue
			}
			var kind string
			if err := json.Unmarshal(kindRaw, &kind); err != nil || kind != name {
				continue
			}
			fieldRaw, ok := item[field]
			if !ok {
				return 0, fmt.Errorf("缺字段")
			}
			var v float64
			if err := json.Unmarshal(fieldRaw, &v); err != nil {
				return 0, fmt.Errorf("字段不是数字")
			}
			return v, nil
		}
	}
	return 0, fmt.Errorf("没找到该条目/字段")
}

func numberField(obj json.RawMessage, field string) (float64, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(obj, &fields); err != nil {
		return 0, false
	}
	raw, ok := fields[field]
	if !ok {
		return 0, false
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false
	}
	return v, true
}

func loadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s 解析失败: %w", path, err)
	}
	if m.AssetRoot == "" {
		return Manifest{}, fmt.Errorf("%s 缺 asset_root", path)
	}
	return m, nil
}
