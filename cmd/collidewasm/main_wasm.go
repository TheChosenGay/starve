//go:build js && wasm

// Command collidewasm 把 pkg/collide 暴露给浏览器：JS 传入场景 JSON，
// Go 侧用碰撞原语计算后返回结果 JSON。用于可视化自测（方案 A）。
//
// 纯逻辑在 logic.go（可在宿主机单测，见 logic_test.go）；本文件只放浏览器胶水。
//
// 构建：GOOS=js GOARCH=wasm go build -o web/collide/collide.wasm ./cmd/collidewasm
package main

import (
	"encoding/json"
	"syscall/js"
)

// eval 是基础图元页的入口：一次查询进出。
func eval(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return `{"error":"no arguments"}`
	}
	var sc scene
	if err := json.Unmarshal([]byte(args[0].String()), &sc); err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	out, err := json.Marshal(compute(sc))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(out)
}

// collideSim 是掉落压力测试页的入口：整场景进、一步出。
func collideSim(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return `{"error":"no arguments"}`
	}
	var in simIn
	if err := json.Unmarshal([]byte(args[0].String()), &in); err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	out, err := json.Marshal(simStep(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(out)
}

// collideCast 是投射场景（子弹 / 劈砍）的入口：球沿 motion 扫掠，对一组目标求首次命中。
func collideCast(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return `{"error":"no arguments"}`
	}
	var in castIn
	if err := json.Unmarshal([]byte(args[0].String()), &in); err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	out, err := json.Marshal(castScene(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(out)
}

// collideSwing 是劈砍场景的入口：刀（OBB）在一个角区间内扫掠，对一组目标求最早命中。
func collideSwing(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return `{"error":"no arguments"}`
	}
	var in swingIn
	if err := json.Unmarshal([]byte(args[0].String()), &in); err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	out, err := json.Marshal(swingScene(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(out)
}

func main() {
	js.Global().Set("collideEval", js.FuncOf(eval))
	js.Global().Set("collideSim", js.FuncOf(collideSim))
	js.Global().Set("collideCast", js.FuncOf(collideCast))
	js.Global().Set("collideSwing", js.FuncOf(collideSwing))
	select {}
}
