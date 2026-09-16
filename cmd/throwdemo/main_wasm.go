//go:build js && wasm

// throw.html 的浏览器胶水层：把演示世界的操作暴露成 JS 函数。
//
// 与 aggro/boss 演示的分工一致——纯逻辑在 throw_logic.go（宿主机可单测），
// 本文件只做 JSON 进出的转发。
//
// 构建：GOOS=js GOARCH=wasm go build -o web/collide/throw.wasm ./cmd/throwdemo
package main

import (
	"encoding/json"
	"syscall/js"
)

var demo = newThrowWorld(defaultProps, defaultBeasts)

const (
	defaultProps  = 8
	defaultBeasts = 10
)

// throwReset 重开一局。args[0]=投掷物数，args[1]=野兽数。
func throwReset(this js.Value, args []js.Value) any {
	p, b := defaultProps, defaultBeasts
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		p = args[0].Int()
	}
	if len(args) > 1 && args[1].Type() == js.TypeNumber {
		b = args[1].Int()
	}
	demo.reset(p, b)
	return mustJSON(demo.snapshot())
}

// throwStep 推进 n tick（默认 1）。
func throwStep(this js.Value, args []js.Value) any {
	n := 1
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		n = args[0].Int()
	}
	if n < 1 {
		n = 1
	}
	if n > 60 {
		n = 60
	}
	for i := 0; i < n; i++ {
		demo.step()
	}
	return mustJSON(demo.snapshot())
}

// throwAim 设置瞄准点（前端点击地图）。
func throwAim(this js.Value, args []js.Value) any {
	if len(args) >= 2 {
		demo.aimAt(args[0].Float(), args[1].Float())
	}
	return mustJSON(demo.snapshot())
}

// throwSelect 选中第 i 件投掷物。
func throwSelect(this js.Value, args []js.Value) any {
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		demo.selectProp(args[0].Int())
	}
	return mustJSON(demo.snapshot())
}

// throwSetStrength 调整力量（前端滑块）。
func throwSetStrength(this js.Value, args []js.Value) any {
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		demo.setStrength(args[0].Int())
	}
	return mustJSON(demo.snapshot())
}

// throwDo 执行投掷（前端按钮）。
func throwDo(this js.Value, args []js.Value) any {
	demo.doThrow()
	return mustJSON(demo.snapshot())
}

// throwSnapshot 只取快照（不推进）。
func throwSnapshot(this js.Value, args []js.Value) any {
	return mustJSON(demo.snapshot())
}

// throwParams 返回演示参数。
func throwParams(this js.Value, args []js.Value) any {
	return mustJSON(map[string]any{
		"props":        defaultProps,
		"beasts":       defaultBeasts,
		"strength":     demoStrength,
		"baseDistance": baseThrowDistance(),
		"blastRadius":  blastRadius(),
		"blastDamage":  blastDamage(),
		"gravity":      gravity(),
	})
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":` + jsonString(err.Error()) + `}`
	}
	return string(b)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func main() {
	js.Global().Set("throwReset", js.FuncOf(throwReset))
	js.Global().Set("throwStep", js.FuncOf(throwStep))
	js.Global().Set("throwAim", js.FuncOf(throwAim))
	js.Global().Set("throwSelect", js.FuncOf(throwSelect))
	js.Global().Set("throwSetStrength", js.FuncOf(throwSetStrength))
	js.Global().Set("throwDo", js.FuncOf(throwDo))
	js.Global().Set("throwSnapshot", js.FuncOf(throwSnapshot))
	js.Global().Set("throwParams", js.FuncOf(throwParams))
	select {}
}
