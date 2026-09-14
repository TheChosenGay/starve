package main

import "time"

// mover 是"按键 → 方向脉冲"的状态机。
//
// 为什么需要它：服务端的移动语义是**方向保持**（按住持续输入、松开清 0），
// 而终端**拿不到松键事件**——所以直接转发按键的话，按一下就会一直走，
// 表现为"整个世界在滚、停不下来"。
//
// 解法：按键只发一次方向，保持 pulse 之后自动发停止。终端的自动重复会在按住时
// 反复送来同一个字节，于是两种情况都符合直觉：
//   - 点一下 → 走一小段（10 格/秒 × 180ms ≈ 2 格）
//   - 按住键 → 自动重复不断续期 → 连续走
//
// hold=true 时退化成旧的"按一下就一直走"（给脚本/习惯用，见 -walk-hold）。
type mover struct {
	dir    [2]int
	until  time.Time
	hold   bool
	facing [2]int // 最近一次移动意图（静止也保留，供 HUD 显示朝向）
}

// press 处理一次按键，返回要发给服务端的方向。
func (m *mover) press(dx, dy int, now time.Time, pulse time.Duration) [2]int {
	m.dir = [2]int{dx, dy}
	m.facing = m.dir
	if m.hold {
		m.until = time.Time{} // 不设期限：一直走
	} else {
		m.until = now.Add(pulse)
	}
	return m.dir
}

// release 立即停止（space / 退出前）。
func (m *mover) release() [2]int {
	m.dir = [2]int{}
	m.until = time.Time{}
	return m.dir
}

// tick 到点了就该发停止；返回 true 表示本次需要发 (0,0)。
// 只会返回一次 true（发完就把状态清干净），避免每帧重复发停止。
func (m *mover) tick(now time.Time) bool {
	if m.dir == [2]int{} || m.until.IsZero() {
		return false
	}
	if now.Before(m.until) {
		return false
	}
	m.dir = [2]int{}
	m.until = time.Time{}
	return true
}

// walking 当前是否处于"正在走"（HUD 显示用）。
func (m *mover) walking(now time.Time) bool {
	if m.dir == [2]int{} {
		return false
	}
	return m.until.IsZero() || now.Before(m.until)
}
