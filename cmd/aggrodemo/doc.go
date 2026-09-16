// Command aggrodemo 把**真实的群体仇恨机制**编译成 WebAssembly，
// 在浏览器里演示"打一只狼，狼群一起扑上来"（aggro.html）。
//
// 与 bossdemo 同构：跑的是真实 ecs.World + 真实系统装配 + 真实行为树 +
// **真实的 Creature.AddThreat / SpreadThreatToAllies**，
// 只是把每 tick 的结果序列化成 JSON 交给浏览器渲染。
//
// 分层（与 collidewasm / bossdemo 一致）：
//   - aggro_logic.go：纯逻辑，**不引用 syscall/js**，可在宿主机单测；
//   - main_wasm.go / main_other.go：浏览器胶水 / 宿主机占位。
package main
