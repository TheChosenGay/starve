package main

import (
	"fmt"
	"strings"
)

// hudState 是状态行要显示的一次性视图状态（相机/叠加层/朝向/最近反馈）。
type hudState struct {
	overlay bool
	cam     camMode
	facing  [2]int
	walking bool
	status  string

	// 本地预测诊断：预测位置与服务端权威位置的偏差（格），以及校正计数。
	// 这是"无头验证三阶段移动"的关键观测量——偏差长期不为 0 说明预测与服务端分叉。
	predicting bool
	predX      float64
	predY      float64
	predErr    float64
	predCorr   int
	predSnaps  int
}

// drawHUD 画状态行与操作提示（占最后两行）。
func drawHUD(f *frame, w *world, st hudState) {
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
	if st.overlay {
		overlayTag = "on"
	}
	walk := "停"
	if st.walking {
		walk = "走"
	}
	line := fmt.Sprintf(" 我 %s %s  朝向 %s  %s  相机 %s  碰撞体 %s  hp %s  tick %d  light %.2f  实体 %d（采 %d/生物 %d/掉落 %d/他人 %d）",
		pos, walk, compass(st.facing), moveHint(st.facing), st.cam, overlayTag,
		hp, w.tick, w.dayLight, len(w.entities), work, creature, loot, other)
	f.put(0, statusY, pad(line, f.w), cBold+cWhite)

	// 第二状态行：本地预测诊断（预测位置 vs 服务端位置）。
	if st.predicting {
		pred := fmt.Sprintf("预测 (%.2f,%.2f)  偏差 %.3f 格  校正 %d  贴合 %d",
			st.predX, st.predY, st.predErr, st.predCorr, st.predSnaps)
		// 偏差健康度上色：<0.1 绿（预测准）；<0.5 黄；否则红（分叉）
		color := cGreen
		if st.predErr >= 0.5 {
			color = cRed
		} else if st.predErr >= 0.1 {
			color = cYellow
		}
		if statusY-1 >= 0 {
			f.put(0, statusY-1, pad(pred, f.w), cBold+color)
		}
	}

	if st.status != "" {
		// 最近一条操作反馈，右对齐覆盖在状态行右侧
		start := f.w - dispWidth(st.status) - 2
		if start > 0 {
			f.put(start, statusY, st.status, cBold+cBrYel)
		}
	}

	help := " WASD/hjkl 走一小段（按住连走）  space 停  f 自动行为  e 作业  p 拾取  c 碰撞体  v 相机  q 退出 "
	f.put(0, helpY, pad(help, f.w), cGray)
}

// compass 把方向变成人话（终端里没有"屏幕上方就是北"的直觉，所以显式写出来）。
func compass(dir [2]int) string {
	names := map[[2]int]string{
		{0, -1}: "北(-Y)", {0, 1}: "南(+Y)", {-1, 0}: "西(-X)", {1, 0}: "东(+X)",
		{-1, -1}: "西北", {1, -1}: "东北", {-1, 1}: "西南", {1, 1}: "东南",
	}
	if n, ok := names[dir]; ok {
		return n
	}
	return "—"
}

// moveHint 方向 → 屏幕上的箭头提示（玩家看的是屏幕，不是格坐标）。
func moveHint(dir [2]int) string {
	arrows := map[[2]int]string{
		{0, -1}: "↑", {0, 1}: "↓", {-1, 0}: "←", {1, 0}: "→",
		{-1, -1}: "↖", {1, -1}: "↗", {-1, 1}: "↙", {1, 1}: "↘",
	}
	if a, ok := arrows[dir]; ok {
		return a
	}
	return "·"
}

// dispWidth 字符串的显示宽度（中文/全角按 2 列）。
func dispWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
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
	cam := &camera{}
	cam.follow(cx, cy, cols, rows-2)
	drawWorld(f, w, cam.view(cols, rows-2), true)
	drawHUD(f, w, hudState{overlay: true, cam: cam.mode}) // 状态位留给交互反馈，dump 不放东西
	fmt.Println(f.render(noColor))
	fmt.Printf("地图 %d×%d  视口中心 (%d,%d)\n", w.width, w.height, cx, cy)
	fmt.Printf("自己: %s\n", ownSummary(w))
	printNeighborhood(w, cx, cy)
}

// printNeighborhood 打印玩家周围的地形与角高度。
// 走路快慢由坡度因子决定，而坡度来自角高度——所以"这里为什么走得慢"要靠它看。
func printNeighborhood(w *world, cx, cy int) {
	const half, rows = 5, 2
	fmt.Printf("周围（#=我；实体行：T 树 / A 岩 / # 建筑 / & 工作站 / o 占位物）：\n")
	for dy := -rows; dy <= rows; dy++ {
		var line, ents, heights strings.Builder
		for dx := -half; dx <= half; dx++ {
			x, y := cx+dx, cy+dy
			ch, _ := terrainCell(w.TileType(x, y))
			line.WriteRune(ch)
			e := w.at(x, y)
			switch {
			case dy == 0 && dx == 0:
				ents.WriteString("#")
			case e == nil:
				ents.WriteString("·")
			default:
				g, _ := entityCell(w, e)
				ents.WriteRune(g)
			}
			heights.WriteString(fmt.Sprintf(" %2d", w.HeightAt(x, y)))
		}
		fmt.Printf("  y=%3d %s | %s | h:%s\n", cy+dy, line.String(), ents.String(), heights.String())
	}
	fmt.Printf("  x=     %s   %s\n", axisLabels(cx-half, half), axisLabels(cx-half, half))
}

// axisLabels 打印列坐标（每列 1 字符，够用）。
func axisLabels(start, n int) string {
	var b strings.Builder
	for i := 0; i <= n; i++ {
		b.WriteString(fmt.Sprintf("%d", (start+i)%10))
	}
	return b.String()
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
