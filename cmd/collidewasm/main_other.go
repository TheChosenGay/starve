//go:build !(js && wasm)

// 非 wasm 平台下的占位 main，保证 `go build ./...` 在宿主机上仍可通过。
package main

func main() {}
