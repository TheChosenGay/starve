//go:build !(js && wasm)

// 宿主机上的占位入口：aggrodemo 只在浏览器里跑（aggro.html）。
//
// 逻辑本身可在宿主机单测（aggro_logic_test.go），但二进制入口只服务 WASM；
// 没有这个文件时 `go build ./...` 会因"main 包缺 main 函数"失败。
package main

func main() {}
