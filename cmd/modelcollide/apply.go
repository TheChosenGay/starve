package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// applyGrants 把流水线推导出来的值写进**手写配置**（resource_templates /
// buildings / creatures 里清单 targets 声明的字段）。
//
// 设计取向：
//   - **默认 dry-run**，只打印"该改什么"；加 -yes 才落盘；
//   - **只做字节级就地替换**：不动其它任何字节，所以 diff 里只剩真正变化的那个数字。
//     先解析再重排会把整个配置文件变成 diff（仓库里 creatures.json 的
//     "drops": [ { ... } ] 是内联写法，一重排就全变），人就没法 review 了；
//   - 不做语义改名（如 blocking → collision_radius），也不新增字段：
//     字段不存在时明确报错，让人手工加。
func applyGrants(root string, grants []Grant, write bool) (changed int, err error) {
	byFile := map[string][]Grant{}
	for _, g := range grants {
		byFile[g.File] = append(byFile[g.File], g)
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	for _, file := range files {
		path := filepath.Join(root, filepath.FromSlash(file))
		src, err := os.ReadFile(path)
		if err != nil {
			return changed, err
		}
		list := byFile[file]
		sort.Slice(list, func(i, j int) bool {
			if list[i].Name != list[j].Name {
				return list[i].Name < list[j].Name
			}
			return list[i].Field < list[j].Field
		})

		type edit struct {
			g          Grant
			start, end int
			old        float64
			hadOld     bool
		}
		var edits []edit
		for _, g := range list {
			start, end, ok, err := findValueSpan(src, g.Name, g.Field)
			if err != nil {
				return changed, fmt.Errorf("%s: %w", file, err)
			}
			if !ok {
				return changed, fmt.Errorf("%s: 找不到 %s.%s（字段要先手工加好；"+
					"-apply 只改已有字段，不新增也不改名）", file, g.Name, g.Field)
			}
			old, hadOld := parseNumber(src[start:end])
			if hadOld && math.Abs(old-g.Value) < 1e-9 {
				continue
			}
			edits = append(edits, edit{g: g, start: start, end: end, old: old, hadOld: hadOld})
		}
		if len(edits) == 0 {
			continue
		}
		// 从后往前替换，避免前面的改动让后面的字节偏移失效
		sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
		for _, e := range edits {
			if e.hadOld {
				fmt.Printf("  %s  %s.%s: %s → %s\n", file, e.g.Name, e.g.Field, numText(e.old), numText(e.g.Value))
			} else {
				fmt.Printf("  %s  %s.%s: (非数字) → %s\n", file, e.g.Name, e.g.Field, numText(e.g.Value))
			}
			out := make([]byte, 0, len(src)+8)
			out = append(out, src[:e.start]...)
			out = append(out, numText(e.g.Value)...)
			out = append(out, src[e.end:]...)
			src = out
			changed++
		}
		if write {
			if err := os.WriteFile(path, src, 0o644); err != nil {
				return changed, err
			}
		}
	}
	return changed, nil
}

// numText 打印数值：整数不带小数点（width: 2 而不是 2.0）。
func numText(v float64) string {
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func parseNumber(raw []byte) (float64, bool) {
	var n json.Number
	if err := json.Unmarshal(bytes.TrimSpace(raw), &n); err != nil {
		return 0, false
	}
	v, err := n.Float64()
	if err != nil {
		return 0, false
	}
	return v, true
}

// ---- 用 json.Decoder 的 InputOffset 求"值的字节区间"，从而就地替换数字 ----

type jsonMember struct {
	key              string
	valStart, valEnd int // 值的字节区间（相对整个 src）
}

// findValueSpan 找"条目 name 的字段 field"的值区间。兼容两种配置形状：
//   - 顶层以名字为键的对象：configs/resource_templates.json → { "wood": {...} }
//   - 数组包装、元素用 kind 标识：configs/creatures.json / buildings.json
//     → { "creatures": [ { "kind": "rabbit", ... } ] }
func findValueSpan(src []byte, name, field string) (int, int, bool, error) {
	members, err := objectMembers(src, 0, len(src))
	if err != nil {
		return 0, 0, false, err
	}
	for _, m := range members {
		if m.key == name {
			inner, err := objectMembers(src, m.valStart, m.valEnd)
			if err != nil {
				return 0, 0, false, nil // 名字撞上非对象字段（例如 _doc）
			}
			return pickField(inner, field)
		}
		elems, err := arrayElements(src, m.valStart, m.valEnd)
		if err != nil {
			continue // 不是数组字段
		}
		for _, el := range elems {
			inner, err := objectMembers(src, el.valStart, el.valEnd)
			if err != nil {
				continue
			}
			if kind, _ := pickString(src, inner, "kind"); kind != name {
				continue
			}
			return pickField(inner, field)
		}
	}
	return 0, 0, false, nil
}

func pickField(members []jsonMember, field string) (int, int, bool, error) {
	for _, m := range members {
		if m.key == field {
			return m.valStart, m.valEnd, true, nil
		}
	}
	return 0, 0, false, nil
}

func pickString(src []byte, members []jsonMember, field string) (string, bool) {
	for _, m := range members {
		if m.key != field {
			continue
		}
		var s string
		if err := json.Unmarshal(src[m.valStart:m.valEnd], &s); err != nil {
			return "", false
		}
		return s, true
	}
	return "", false
}

// objectMembers 解析 [start,end) 区间里的 JSON 对象，给出每个成员"值"的字节区间。
func objectMembers(src []byte, start, end int) ([]jsonMember, error) {
	dec := json.NewDecoder(bytes.NewReader(src[start:end]))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("区间 [%d,%d) 不是 JSON 对象", start, end)
	}
	var out []jsonMember
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		afterKey := int(dec.InputOffset()) // 相对本区间，停在**本键**的右引号之后
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		afterVal := int(dec.InputOffset())
		// 只在"本键之后"找冒号：从对象开头找会命中前一个字段的冒号，
		// 把 valStart 指到别的字段的值上（那是会改错数据的 bug）。
		colon := bytes.IndexByte(src[start+afterKey:start+afterVal], ':')
		valStart := start + afterVal
		if colon >= 0 {
			valStart = start + afterKey + colon + 1
			for valStart < start+afterVal {
				switch src[valStart] {
				case ' ', '\t', '\n', '\r':
					valStart++
					continue
				}
				break
			}
		}
		out = append(out, jsonMember{key: key, valStart: valStart, valEnd: start + afterVal})
	}
	return out, nil
}

// arrayElements 解析 [start,end) 区间里的 JSON 数组；不是数组就报错。
func arrayElements(src []byte, start, end int) ([]jsonMember, error) {
	dec := json.NewDecoder(bytes.NewReader(src[start:end]))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("区间 [%d,%d) 不是 JSON 数组", start, end)
	}
	var out []jsonMember
	for dec.More() {
		before := int(dec.InputOffset()) // 元素前的空白/逗号起点（本区间内）
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		after := int(dec.InputOffset())
		// 注意：数组元素要跳过的是空白与逗号，**不能**用 valueStart
		// （那会去找第一个冒号，而冒号属于元素内部第一个字段）。
		out = append(out, jsonMember{
			valStart: start + before + skipJSONLeading(src[start+before:start+after]),
			valEnd:   start + after,
		})
	}
	return out, nil
}

// skipJSONLeading 跳过 JSON 元素前的空白与逗号，返回元素起始下标。
func skipJSONLeading(b []byte) int {
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case ' ', '\t', '\n', '\r', ',':
			continue
		}
		return i
	}
	return len(b)
}

// valueStart 在"键/元素之后的片段"里跳过冒号与空白，返回值起始下标。
func valueStart(prefix []byte) int {
	i := bytes.IndexByte(prefix, ':')
	if i < 0 {
		// 数组元素：跳过前导空白
		i = -1
		for j := 0; j < len(prefix); j++ {
			switch prefix[j] {
			case ' ', '\t', '\n', '\r', ',':
				continue
			}
			return j
		}
		return len(prefix)
	}
	i++
	for i < len(prefix) && (prefix[i] == ' ' || prefix[i] == '\t' || prefix[i] == '\n' || prefix[i] == '\r') {
		i++
	}
	return i
}
