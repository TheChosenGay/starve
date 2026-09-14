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

// 宽阶段压测页（broad.html）的三个入口：建场景 / 推进一步 / 跑一批查询。
func collideBPSetup(this js.Value, args []js.Value) any {
	var in bpSetupIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"ok":false,"error":"bad arguments"}`
	}
	b, err := json.Marshal(bpSetup(in))
	if err != nil {
		return `{"ok":false,"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

func collideBPStep(this js.Value, args []js.Value) any {
	var in bpStepIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"error":"bad arguments"}`
	}
	b, err := json.Marshal(bpStep(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

func collideBPQuery(this js.Value, args []js.Value) any {
	var in bpQueryIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"error":"bad arguments"}`
	}
	b, err := json.Marshal(bpQuery(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

// 挥砍场景页（swing.html）的两个入口。
func collideSectorSetup(this js.Value, args []js.Value) any {
	var in sectorSetupIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"ok":false,"error":"bad arguments"}`
	}
	ok := sectorSetup(in)
	if ok {
		return `{"ok":true}`
	}
	return `{"ok":false}`
}

func collideSectorStep(this js.Value, args []js.Value) any {
	var in sectorStepIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"error":"bad arguments"}`
	}
	b, err := json.Marshal(sectorStep(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

// 竖劈竞技场页（arena.html）的三个入口。
func collideArenaSetup(this js.Value, args []js.Value) any {
	var in arenaSetupIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"ok":false,"error":"bad arguments"}`
	}
	if arenaSetup(in) {
		return `{"ok":true}`
	}
	return `{"ok":false}`
}

func collideArenaStep(this js.Value, args []js.Value) any {
	var in arenaStepIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"error":"bad arguments"}`
	}
	b, err := json.Marshal(arenaStep(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

func collideArenaSwing(this js.Value, args []js.Value) any {
	var in arenaSwingIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"error":"bad arguments"}`
	}
	b, err := json.Marshal(arenaSwing(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

// 丢炸弹页（blast.html）的四个入口：建场景 / 瞄准 / 投掷 / 推进一步。
// 瞄准与投掷都吃同一条鼠标射线，几何部分（射线 → 地面落点、落点 → 球形爆炸范围）
// 全在 Go 侧算，前端只负责把屏幕坐标换成世界空间的射线。
func collideBlastSetup(this js.Value, args []js.Value) any {
	var in blastSetupIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"ok":false,"error":"bad arguments"}`
	}
	if blastSetup(in) {
		return `{"ok":true}`
	}
	return `{"ok":false}`
}

func collideBlastAim(this js.Value, args []js.Value) any {
	var in blastRayIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"ok":false,"error":"bad arguments"}`
	}
	b, err := json.Marshal(blastAim(in))
	if err != nil {
		return `{"ok":false,"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

func collideBlastThrow(this js.Value, args []js.Value) any {
	var in blastThrowIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"ok":false,"error":"bad arguments"}`
	}
	b, err := json.Marshal(blastThrow(in))
	if err != nil {
		return `{"ok":false,"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

func collideBlastStep(this js.Value, args []js.Value) any {
	var in blastStepIn
	if len(args) == 0 || json.Unmarshal([]byte(args[0].String()), &in) != nil {
		return `{"error":"bad arguments"}`
	}
	b, err := json.Marshal(blastStep(in))
	if err != nil {
		return `{"error":` + mustJSON(err.Error()) + `}`
	}
	return string(b)
}

func main() {
	js.Global().Set("collideEval", js.FuncOf(eval))
	js.Global().Set("collideSim", js.FuncOf(collideSim))
	js.Global().Set("collideCast", js.FuncOf(collideCast))
	js.Global().Set("collideSwing", js.FuncOf(collideSwing))
	js.Global().Set("collideBPSetup", js.FuncOf(collideBPSetup))
	js.Global().Set("collideBPStep", js.FuncOf(collideBPStep))
	js.Global().Set("collideBPQuery", js.FuncOf(collideBPQuery))
	js.Global().Set("collideSectorSetup", js.FuncOf(collideSectorSetup))
	js.Global().Set("collideSectorStep", js.FuncOf(collideSectorStep))
	js.Global().Set("collideArenaSetup", js.FuncOf(collideArenaSetup))
	js.Global().Set("collideArenaStep", js.FuncOf(collideArenaStep))
	js.Global().Set("collideArenaSwing", js.FuncOf(collideArenaSwing))
	js.Global().Set("collideBlastSetup", js.FuncOf(collideBlastSetup))
	js.Global().Set("collideBlastAim", js.FuncOf(collideBlastAim))
	js.Global().Set("collideBlastThrow", js.FuncOf(collideBlastThrow))
	js.Global().Set("collideBlastStep", js.FuncOf(collideBlastStep))
	select {}
}
