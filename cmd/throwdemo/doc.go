// Command throwdemo 把**真实的投掷机制**编译成 WebAssembly，
// 在浏览器里演示"瞄准 → 蓄力 → 抛出 → 抛物线飞行 → 落地爆炸"（throw.html）。
//
// 与 aggro/boss 演示同构：跑的是真实 ecs.World + 真实系统装配 +
// **真实的 ThrowBehavior.CanThrow / Throw + ThrowSystem 飞行与落地结算**，
// 只是把每 tick 的结果序列化成 JSON 交给浏览器渲染。
//
// 分层（与 collidewasm / bossdemo / aggrodemo 一致）：
//   - throw_logic.go：纯逻辑，**不引用 syscall/js**，可在宿主机单测；
//   - main_wasm.go / main_other.go：浏览器胶水 / 宿主机占位。
package main
