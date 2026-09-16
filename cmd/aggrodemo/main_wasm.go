//go:build js && wasm

// aggro.html 的浏览器胶水层：把演示世界的操作暴露成 JS 函数。
//
// 与 bossdemo 的分工一致——纯逻辑在 aggro_logic.go（宿主机可单测），
// 本文件只做 JSON 进出的转发。
//
// 构建：GOOS=js GOARCH=wasm go build -o web/collide/aggro.wasm ./cmd/aggrodemo
package main

import (
	"encoding/json"
	"syscall/js"
)

// 全局演示世界（浏览器里只跑一局）。
var demo = newAggroWorld(defaultWolfCount)

const defaultWolfCount = 6

// aggroReset 重开一局。
func aggroReset(this js.Value, args []js.Value) any {
	n := defaultWolfCount
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		n = args[0].Int()
	}
	demo.reset(n)
	return mustJSON(demo.snapshot())
}

// aggroStep 推进 n tick（默认 1），返回新快照。
//
// 前端按动画帧调用（每帧推进若干 tick 以追上 20Hz 的模拟节奏）。
func aggroStep(this js.Value, args []js.Value) any {
	n := 1
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		n = args[0].Int()
	}
	if n < 1 {
		n = 1
	}
	if n > 60 {
		n = 60 // 上限：避免标签页切回后一次性补几千 tick
	}
	for i := 0; i < n; i++ {
		demo.step()
	}
	return mustJSON(demo.snapshot())
}

// aggroAttack 让玩家攻击第 idx 只狼（前端点击某只狼）。
// 这是触发群体仇恨的入口。
func aggroAttack(this js.Value, args []js.Value) any {
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		demo.attackWolf(args[0].Int())
	}
	return mustJSON(demo.snapshot())
}

// aggroMovePlayer 把玩家移动到指定坐标（前端拖动/点击空地）。
func aggroMovePlayer(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return mustJSON(demo.snapshot())
	}
	demo.movePlayer(args[0].Float(), args[1].Float())
	return mustJSON(demo.snapshot())
}

// aggroSetWolfCount 重建狼群（前端"狼群规模"滑块）。
func aggroSetWolfCount(this js.Value, args []js.Value) any {
	n := defaultWolfCount
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		n = args[0].Int()
	}
	demo.setWolfCount(n)
	return mustJSON(demo.snapshot())
}

// aggroSnapshot 只取快照（不推进）。
func aggroSnapshot(this js.Value, args []js.Value) any {
	return mustJSON(demo.snapshot())
}

// aggroParams 返回演示参数（前端画场地/范围提示用）。
func aggroParams(this js.Value, args []js.Value) any {
	return mustJSON(map[string]any{
		"wolfCount":    defaultWolfCount,
		"aoiRadius":    demoAoiRadius,
		"playerDamage": demoPlayerDamage,
		"fieldHalf":    demoFieldHalf,
		"origin":       demoOrigin,
		"wolfHP":       demoWolfHP,
		"wolfDamage":   demoWolfDamage,
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
	js.Global().Set("aggroReset", js.FuncOf(aggroReset))
	js.Global().Set("aggroStep", js.FuncOf(aggroStep))
	js.Global().Set("aggroAttack", js.FuncOf(aggroAttack))
	js.Global().Set("aggroMovePlayer", js.FuncOf(aggroMovePlayer))
	js.Global().Set("aggroSetWolfCount", js.FuncOf(aggroSetWolfCount))
	js.Global().Set("aggroSnapshot", js.FuncOf(aggroSnapshot))
	js.Global().Set("aggroParams", js.FuncOf(aggroParams))
	select {}
}
