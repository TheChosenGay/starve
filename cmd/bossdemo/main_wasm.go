//go:build js && wasm

// boss.html 的浏览器胶水层：把演示世界的操作暴露成 JS 函数。
//
// 与 collidewasm 的分工一致——纯逻辑在 boss_logic.go（宿主机可单测），
// 本文件只做 JSON 进出的转发。
//
// 构建：GOOS=js GOARCH=wasm go build -o web/collide/boss.wasm ./cmd/bossdemo
package main

import (
	"encoding/json"
	"syscall/js"
)

// 全局演示世界（浏览器里只跑一局）。
var demo = newBossWorld()

// bossReset 重开一局。
func bossReset(this js.Value, args []js.Value) any {
	demo.reset()
	return mustJSON(demo.snapshot())
}

// bossStep 推进 n tick（默认 1），返回新快照。
//
// 前端按动画帧调用（每帧推进若干 tick 以追上 20Hz 的模拟节奏）。
func bossStep(this js.Value, args []js.Value) any {
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

// bossDamage 给 Boss 造成伤害（前端"攻击 Boss"按钮，用于推进到二阶段）。
func bossDamage(this js.Value, args []js.Value) any {
	amount := 20
	if len(args) > 0 && args[0].Type() == js.TypeNumber {
		amount = args[0].Int()
	}
	demo.damageBoss(amount)
	return mustJSON(demo.snapshot())
}

// bossMovePlayer 把玩家移动到指定坐标（前端拖动）。
func bossMovePlayer(this js.Value, args []js.Value) any {
	if len(args) < 2 {
		return mustJSON(demo.snapshot())
	}
	demo.movePlayer(args[0].Float(), args[1].Float())
	return mustJSON(demo.snapshot())
}

// bossSnapshot 只取快照（不推进）。
func bossSnapshot(this js.Value, args []js.Value) any {
	return mustJSON(demo.snapshot())
}

// bossTree 返回行为树的结构描述（前端侧栏展示"决策树长什么样"）。
func bossTree(this js.Value, args []js.Value) any {
	return mustJSON(demo.treeDescription())
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
	js.Global().Set("bossReset", js.FuncOf(bossReset))
	js.Global().Set("bossStep", js.FuncOf(bossStep))
	js.Global().Set("bossDamage", js.FuncOf(bossDamage))
	js.Global().Set("bossMovePlayer", js.FuncOf(bossMovePlayer))
	js.Global().Set("bossSnapshot", js.FuncOf(bossSnapshot))
	js.Global().Set("bossTree", js.FuncOf(bossTree))
	select {}
}
