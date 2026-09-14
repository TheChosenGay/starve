package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"starve/pkg/modelcollide"
)

// scaffold 是"新模型自动登记"：扫描未登记模型，按三个信号猜一条候选清单条目，
// 让 CI 能直接开出一个带候选条目的 PR——人只需要确认 scale 与 band 两个字段。
//
// 三个信号（优先级从高到低）：
//  1. **同目录已登记条目**（最强）：shape / capsule_axis / band / percentile / targets
//     直接抄同目录同形状的那条，scale 取同目录中位数（同一批资产通常同一套缩放约定）；
//  2. **几何比例**：横长比（XZ 长边/短边）≥1.5 → 四足 body 胶囊；竖长比（高/横长）≥1.5
//     → 直立胶囊；两个方向都接近 1 且矮 → 占格盒；其余 → 格心圆。
//     同目录没有参考时只能靠它，并且会在 note 里标明"这是猜的"。
//  3. **命名/路径**：只用于生成 entity 名与提示，不参与判定（不可靠）。
//
// **猜不出来的必须留 TODO**：
//   - `scale` 必须等于客户端渲染该模型的 `ModelScale` 常量（那是人在客户端代码里填的）；
//   - `band` 是"树干挡人还是整棵树挡人"的手感决策，几何上两种都合理。
//
// 另外：**全新生物种类**要先在 proto 里加 `CreatureKind` 枚举值并写 `creatures.json` 条目。
// 在那之前 `targets` 不能写——写了 `-apply` 会因为找不到条目而失败、整个 CI 红掉——
// 所以这里会**自动省略 targets** 并把原因写进 note，让 PR 照常开出来。
func runScaffold(root string, m Manifest, assetRoot string, write bool) (int, error) {
	unregistered, err := scanAssets(m, assetRoot, false)
	if err != nil {
		return 0, err
	}
	if len(unregistered) == 0 {
		fmt.Println("没有未登记模型，无需 scaffold")
		return 0, nil
	}

	// 配置里已存在的条目名：决定 targets 能不能落地（`-apply` 只改已有字段）。
	names := map[string]map[string]bool{}
	for _, kind := range []string{"creature", "resource_template", "building"} {
		set, err := configEntryNames(root, configFileOf(kind))
		if err != nil {
			return 0, err
		}
		names[kind] = set
	}
	hasEntry := func(kind, name string) bool { return names[kind][name] }

	var entries []Entry
	for _, rel := range unregistered {
		mesh, err := modelcollide.LoadGLB(filepath.Join(assetRoot, filepath.FromSlash(rel)))
		if err != nil {
			fmt.Printf("跳过 %s：%v\n", rel, err)
			continue
		}
		entry, summary, notes := buildCandidate(rel, mesh, m, hasEntry)
		entries = append(entries, entry)
		fmt.Printf("候选 %s → entity=%q（%s）\n", rel, entry.Entity, summary)
		for _, n := range notes {
			fmt.Printf("    ⚠ %s\n", n)
		}
	}
	if len(entries) == 0 {
		return 0, nil
	}

	if !write {
		fmt.Printf("\n共 %d 条候选（dry-run）：确认后加 -yes 写进 %s\n", len(entries), manifestPath)
		return 0, nil
	}
	n, err := insertManifestEntries(root, entries)
	if err != nil {
		return 0, err
	}
	fmt.Printf("\n已写入 %d 条候选到 %s（scale/band 是 TODO，需要人工确认）\n", n, manifestPath)
	return n, nil
}

// buildCandidate 猜一条候选条目，并返回需要人注意的提示。
func buildCandidate(rel string, mesh *modelcollide.Mesh, m Manifest, hasEntry func(kind, name string) bool) (Entry, string, []string) {
	var notes []string
	dir := filepath.ToSlash(filepath.Dir(rel))
	entity := slugify(strings.TrimSuffix(filepath.Base(rel), filepath.Ext(rel)))

	var sibs []Entry
	for _, e := range m.Models {
		if e.Model != "" && filepath.ToSlash(filepath.Dir(e.Model)) == dir {
			sibs = append(sibs, e)
		}
	}

	shape, axis := "circle", ""
	band := [2]float64{0, 1}
	pct := 0.98
	var targets []Target
	scale := 1.0
	scaleSrc := "TODO：抄客户端渲染该模型的 ModelScale 出处（同目录没有参考，scale 暂填 1）"

	if len(sibs) > 0 {
		base := pickSibling(sibs)
		shape, axis = base.Shape, base.CapsuleAxis
		band, pct = base.Band, base.Percentile
		targets = base.Targets
		if band[0] == 0 && band[1] == 0 {
			band = defaultBand(shape, axis)
		}
		if pct <= 0 {
			pct = 0.98
		}
		scale = medianScale(sibs)
		scaleSrc = fmt.Sprintf("TODO：抄客户端该模型的 ModelScale（同目录 %s 用的是 %.3f）", base.Model, scale)
	} else {
		shape, axis, band = guessByGeometry(mesh)
		notes = append(notes, "同目录没有已登记条目可比对：shape/band 是按**几何比例**猜的，务必人工确认")
	}
	if shape != "capsule" {
		axis = ""
	}

	// targets 能不能落地：目标配置里得有这个条目，否则 -apply 会失败、CI 直接红。
	type targetKey struct{ kind, name string }
	var kept []Target
	droppedFields := map[targetKey]int{}
	var droppedOrder []targetKey
	for _, t := range targets {
		name := t.Name
		if name == "" {
			name = entity
		}
		if hasEntry(t.Kind, name) {
			c := t
			c.Name = name
			kept = append(kept, c)
			continue
		}
		k := targetKey{t.Kind, name}
		if droppedFields[k] == 0 {
			droppedOrder = append(droppedOrder, k)
		}
		droppedFields[k]++
	}
	if len(droppedOrder) > 0 {
		var parts []string
		for _, k := range droppedOrder {
			parts = append(parts, fmt.Sprintf("%s/%s（%d 个字段）", k.kind, k.name, droppedFields[k]))
		}
		notes = append(notes, fmt.Sprintf(
			"targets 暂时省略：%s —— 对应配置里还没有这个条目。新实体（尤其新生物种类）要先在 proto "+
				"加枚举值并写好配置条目，再把 targets 照抄同目录的补上", strings.Join(parts, "、")))
	}

	// 按猜出来的规则真推导一次：候选条目里要带上结果，人才好判断这套猜测靠不靠谱。
	summary := "无法按猜的规则推导"
	if p, err := modelcollide.Derive(mesh, modelcollide.Rules{
		Scale: scale, Kind: shape, Band: band, Percentile: pct, Axis: axis,
	}); err == nil {
		summary = fmt.Sprintf("按 scale=%.3f 推导：半径 %.3f", scale, p.Radius)
		switch shape {
		case "capsule":
			summary += fmt.Sprintf(" / 身高 %.3f / 胶囊半长 %.3f", p.Height, p.Capsule.HalfLength())
		case "box":
			summary += fmt.Sprintf(" / 占格 %d×%d", p.BoxTiles[0], p.BoxTiles[1])
		}
		summary += fmt.Sprintf("（包含率 %.1f%%，形心偏 %.3f,%.3f，离地 %+.3f）",
			p.OutsideRatio*100, p.CenterOffset[0], p.CenterOffset[1], p.Bounds[1][0])
		if shape == "capsule" && axis == "body" && p.LongAxis != "z" {
			notes = append(notes, "⚠️ 该模型水平长轴在 "+strings.ToUpper(p.LongAxis)+
				"：客户端把**局部 +Z** 对准朝向，这样服务端碰撞会比渲染差 90°——"+
				"需要美术把模型转成 +Z 朝前再导出（这是硬门禁，CI 会失败）")
		}
	} else {
		notes = append(notes, "推导失败："+err.Error())
	}

	note := "自动生成的候选条目（cmd/modelcollide -scaffold）：" + summary
	if len(notes) > 0 {
		note += "；" + strings.Join(notes, "；")
	}
	note += "。scale 与 band 必须人工确认。"

	return Entry{
		Entity: entity, Model: rel, Scale: scale, ScaleSource: scaleSrc,
		Shape: shape, Band: band, Percentile: pct, CapsuleAxis: axis,
		Targets: kept, Note: note,
	}, summary, notes
}

// pickSibling 在同目录条目里选"最有代表性"的一条：形状占多数的那条（平局取形状名小的，
// 保证确定性），后面几个字段都从它抄。
func pickSibling(sibs []Entry) Entry {
	count := map[string]int{}
	for _, e := range sibs {
		count[e.Shape]++
	}
	best, bestN := sibs[0].Shape, -1
	for s, n := range count {
		if n > bestN || (n == bestN && s < best) {
			best, bestN = s, n
		}
	}
	for _, e := range sibs {
		if e.Shape == best {
			return e
		}
	}
	return sibs[0]
}

// medianScale 同目录条目的 scale 中位数（忽略没填的）。
func medianScale(sibs []Entry) float64 {
	var xs []float64
	for _, e := range sibs {
		if e.Scale > 0 {
			xs = append(xs, e.Scale)
		}
	}
	if len(xs) == 0 {
		return 1
	}
	sort.Float64s(xs)
	return xs[len(xs)/2]
}

// guessByGeometry 同目录没有参考时的兜底：按包围盒比例猜形状。
func guessByGeometry(mesh *modelcollide.Mesh) (shape, axis string, band [2]float64) {
	dx := mesh.Max.X - mesh.Min.X
	dz := mesh.Max.Z - mesh.Min.Z
	dy := mesh.Max.Y - mesh.Min.Y
	long, short := dx, dz
	if dz > dx {
		long, short = dz, dx
	}
	if long <= 0 || short <= 0 || dy <= 0 {
		return "circle", "", [2]float64{0, 1}
	}
	switch {
	case long/short >= 1.5: // 长条 → 四足
		return "capsule", "body", [2]float64{0.25, 0.75}
	case dy/long >= 1.5: // 高瘦 → 人形/直立
		return "capsule", "vertical", [2]float64{0.25, 0.75}
	case dy < long && long/short < 1.3: // 矮而方正 → 建筑
		return "box", "", [2]float64{0, 1}
	default: // 其余当树/石这类格心圆
		return "circle", "", [2]float64{0, 1}
	}
}

func defaultBand(shape, axis string) [2]float64 {
	if shape == "capsule" {
		return [2]float64{0.25, 0.75}
	}
	return [2]float64{0, 1}
}

// slugify 文件名 → entity 名：小写 + 非字母数字折成下划线。
// "White Horse" → "white_horse"；"ghibli_tree_godot" → "ghibli_tree_godot"。
func slugify(s string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// configEntryNames 收集某配置文件里已存在的条目名：根对象的键（resource_templates），
// 或数组元素里的 kind（creatures / buildings）。
func configEntryNames(root, file string) (map[string]bool, error) {
	raw, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s 解析失败: %w", file, err)
	}
	out := map[string]bool{}
	for k, v := range doc {
		if strings.HasPrefix(k, "_") {
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) == nil {
			out[k] = true
			continue
		}
		var list []map[string]json.RawMessage
		if json.Unmarshal(v, &list) != nil {
			continue
		}
		for _, item := range list {
			var kind string
			if k, ok := item["kind"]; ok && json.Unmarshal(k, &kind) == nil && kind != "" {
				out[kind] = true
			}
		}
	}
	return out, nil
}

// insertManifestEntries 把候选条目**就地插入** configs/models.json 的 models 数组末尾。
//
// 与 -apply 同一个取向：只插新字节、不动其它任何字节。先解析再整体重排会把
// 整个清单变成 diff（手工维护的文件要能 review），所以这里用字节区间拼接。
func insertManifestEntries(root string, entries []Entry) (int, error) {
	path := filepath.Join(root, manifestPath)
	src, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}

	members, err := objectMembers(src, 0, len(src))
	if err != nil {
		return 0, err
	}
	var span *jsonMember
	for i := range members {
		if members[i].key == "models" {
			span = &members[i]
			break
		}
	}
	if span == nil {
		return 0, fmt.Errorf("%s 里没有 models 数组", manifestPath)
	}

	closeIdx := -1
	for i := span.valEnd - 1; i >= span.valStart; i-- {
		if src[i] == ']' {
			closeIdx = i
			break
		}
	}
	if closeIdx < 0 {
		return 0, fmt.Errorf("%s 的 models 数组没有闭合的 ]", manifestPath)
	}

	// 数组是否为空：跳过空白往前看最后一个有效字符
	last := -1
	for i := closeIdx - 1; i >= span.valStart; i-- {
		if !isSpace(src[i]) {
			last = i
			break
		}
	}
	empty := last < 0 || src[last] == '['

	indent := elementIndent(src, span.valStart, closeIdx, empty)
	closeIndent := lineIndent(src, closeIdx)

	// 先把 `]` 之前的空白裁掉，自己按缩进补回——否则插进去的逗号会落在
	// 残留的缩进后面，变成「  ,」独占一行。
	var buf bytes.Buffer
	buf.Write(bytes.TrimRight(src[:closeIdx], " \t\r\n"))
	for i, e := range entries {
		raw, err := marshalEntryIndented(e, indent)
		if err != nil {
			return 0, err
		}
		if i == 0 && empty {
			buf.WriteString("\n" + raw)
			continue
		}
		buf.WriteString(",\n" + raw)
	}
	buf.WriteString("\n" + closeIndent)
	buf.Write(src[closeIdx:])

	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// elementIndent 推出数组元素的缩进：非空时看最后一个元素那行的缩进，
// 空数组时用数组自身的缩进 + 2。
func elementIndent(src []byte, start, closeIdx int, empty bool) string {
	if !empty {
		// 从 closeIdx 往前找最后一个非空白字符（应为 }），再回到它所在行的行首
		last := closeIdx - 1
		for last > start && isSpace(src[last]) {
			last--
		}
		lineStart := last
		for lineStart > start && src[lineStart-1] != '\n' {
			lineStart--
		}
		var ind []byte
		for i := lineStart; i <= last && isSpace(src[i]); i++ {
			ind = append(ind, src[i])
		}
		if len(ind) > 0 {
			return string(ind)
		}
	}
	// 空数组：数组那一行的缩进 + 2
	lineStart := start
	for lineStart > 0 && src[lineStart-1] != '\n' {
		lineStart--
	}
	base := ""
	for i := lineStart; i < start && isSpace(src[i]); i++ {
		base += string(src[i])
	}
	return base + "  "
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\r' }

// lineIndent 返回 idx 所在行从行首到 idx 的空白（即该行的缩进）。
func lineIndent(src []byte, idx int) string {
	start := idx
	for start > 0 && src[start-1] != '\n' {
		start--
	}
	var ind []byte
	for i := start; i < idx && isSpace(src[i]); i++ {
		ind = append(ind, src[i])
	}
	return string(ind)
}

// marshalEntryIndented 序列化一条条目并按 indent 缩进（JSON 不转义 HTML 字符）。
func marshalEntryIndented(e Entry, indent string) (string, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		return "", err
	}
	body := strings.TrimRight(b.String(), "\n")
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n"), nil
}
