// Command bossdemo 把**真实 ECS + 行为树 + Boss 三阶段 AI** 编译成 WebAssembly，
// 在浏览器里演示 Boss 行为（boss.html）。
//
// 与 collidewasm 的区别：那些页面是**纯几何**演示（前端给射线、Go 算碰撞）；
// 这一页跑的是**真的世界**：真实的 ecs.World + 真实系统装配 + 真实行为树，
// 只是把"每 tick 的结果"序列化成 JSON 交给浏览器渲染。
//
// 分层（与 collidewasm 一致）：
//   - boss_logic.go：纯逻辑，**不引用 syscall/js**，可在宿主机单测（boss_logic_test.go）；
//   - main_wasm.go / main_other.go：浏览器胶水 / 宿主机占位。
package main
