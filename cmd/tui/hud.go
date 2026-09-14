package main

import (
	"fmt"
	"strings"
)

// drawHUD 画状态行与操作提示（占最后两行）。
func drawHUD(f *frame, w *world, overlay bool, status string) {
	statusY := f.h - 2
	helpY := f.h - 1

	own := w.ownEntity()
	pos := "?"
	if own != nil && own.pos != nil {
		pos = fmt.Sprintf("(%d,%d)", own.tileX(), own.tileY())
	}
	hp := "-"
	if own != nil && own.health != nil {
		hp = fmt.Sprintf("%d/%d", own.health.Cur, own.health.Max)
	}
	work, creature, loot, other := w.counts()
	overlayTag := "off"
	if overlay {
		overlayTag = "on"
	}
	line := fmt.Sprintf(" %s  hp %s  tick %d  light %.2f  实体 %d（采 %d/生物 %d/掉落 %d/他人 %d）  碰撞体 %s ",
		pos, hp, w.tick, w.dayLight, len(w.entities), work, creature, loot, other, overlayTag)
	f.put(0, statusY, pad(line, f.w), cBold+cWhite)

	if status != "" {
		// 最近一条操作反馈，右对齐覆盖在状态行右侧
		start := f.w - len([]rune(status)) - 2
		if start > 0 {
			f.put(start, statusY, status, cBold+cBrYel)
		}
	}

	help := " WASD/hjkl 移动  space 停  f 自动行为  e 作业  p 拾取  c 碰撞体  q 退出 "
	f.put(0, helpY, pad(help, f.w), cGray)
}

// pad 把字符串补齐/裁剪到宽度 n（按 rune 计，中文按 2 列估算）。
func pad(s string, n int) string {
	width := 0
	var b strings.Builder
	for _, r := range s {
		rw := runeWidth(r)
		if width+rw > n {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	for width < n {
		b.WriteByte(' ')
		width++
	}
	return b.String()
}

// renderDump 以 -dump 模式打印一屏（不进入 TUI 主循环，不需要 TTY）。
func renderDump(w *world, vw, vh int, noColor bool) {
	cols, rows := vw, vh
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	// 留出 HUD 两行
	f := newFrame(cols, rows)
	own := w.ownEntity()
	cx, cy := w.width/2, w.height/2
	if own != nil && own.pos != nil {
		cx, cy = own.tileX(), own.tileY()
	}
	drawWorld(f, w, centerView(cx, cy, cols, rows-2), true)
	drawHUD(f, w, true, "") // 状态位留给交互反馈，dump 不放东西
	fmt.Println(f.render(noColor))
	fmt.Printf("地图 %d×%d  视口中心 (%d,%d)\n", w.width, w.height, cx, cy)
	fmt.Printf("自己: %s\n", ownSummary(w))
}

// ownSummary 打印自己身上值得看的组件（-dump 的附注）。
func ownSummary(w *world) string {
	own := w.ownEntity()
	if own == nil {
		return "（未找到自己的实体）"
	}
	parts := []string{}
	if own.moveable != nil {
		parts = append(parts, fmt.Sprintf("speed=%.1f eff=%.1f dir=(%d,%d) sub=(%.2f,%.2f)",
			own.moveable.Speed, own.moveable.EffectiveSpeed,
			own.moveable.DirX, own.moveable.DirY, own.moveable.SubX, own.moveable.SubY))
	}
	if own.shape != nil {
		parts = append(parts, fmt.Sprintf("shape=%s r=%.3f a=(%.2f,%.2f,%.2f) b=(%.2f,%.2f,%.2f)",
			shortEnum(own.shape.Kind.String()), own.shape.Radius,
			own.shape.AX, own.shape.AY, own.shape.AZ, own.shape.BX, own.shape.BY, own.shape.BZ))
	}
	if own.health != nil {
		parts = append(parts, fmt.Sprintf("hp=%d/%d", own.health.Cur, own.health.Max))
	}
	if len(parts) == 0 {
		return "（还没有组件）"
	}
	return strings.Join(parts, "  ")
}
