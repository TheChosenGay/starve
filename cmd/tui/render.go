package main

import (
	"fmt"
	"math"
	"strings"

	game "starve/pkg/proto/game"
)

// ---- ANSI 颜色（只用 8 色 + 亮色，兼容性最好；-no-color 时全部关掉）----

const (
	cReset  = "\x1b[0m"
	cDim    = "\x1b[2m"
	cBold   = "\x1b[1m"
	cRed    = "\x1b[31m"
	cGreen  = "\x1b[32m"
	cYellow = "\x1b[33m"
	cBlue   = "\x1b[34m"
	cMag    = "\x1b[35m"
	cCyan   = "\x1b[36m"
	cWhite  = "\x1b[37m"
	cGray   = "\x1b[90m"
	cBrRed  = "\x1b[91m"
	cBrGrn  = "\x1b[92m"
	cBrYel  = "\x1b[93m"
	cBrBlu  = "\x1b[94m"
	cBrCyn  = "\x1b[96m"
)

// cell 是一格屏幕内容。
// cell 是一格屏幕内容。cont 标记"这是宽字符（中文/全角）占的第二列"，
// 渲染时跳过——否则一个 rune 占两列会让整行右移、把布局挤坏。
type cell struct {
	ch   rune
	fg   string
	bg   string
	cont bool
}

// runeWidth 该字符在终端占几列（只区分"宽"和"窄"，够用且不依赖 wcwidth 表）。
func runeWidth(r rune) int {
	switch {
	case r < 0x1100:
		return 1
	case r >= 0x1100 && r <= 0x115F, // 谚文字母
		r == 0x2329 || r == 0x232A,
		r >= 0x2E80 && r <= 0xA4CF && r != 0x303F, // CJK 部首/汉字/假名等
		r >= 0xAC00 && r <= 0xD7A3,                // 谚文音节
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60, // 全角
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	default:
		return 1
	}
}

// frame 是字符帧缓冲：先铺满，再画实体与叠加层，最后一次性输出（避免闪烁）。
type frame struct {
	w, h  int
	cells []cell
}

func newFrame(w, h int) *frame {
	f := &frame{w: w, h: h, cells: make([]cell, w*h)}
	f.clear()
	return f
}

func (f *frame) clear() {
	for i := range f.cells {
		f.cells[i] = cell{ch: ' ', fg: cReset}
	}
}

func (f *frame) set(x, y int, ch rune, fg string) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	f.cells[y*f.w+x] = cell{ch: ch, fg: fg}
}

// setFgBg 同时写前景与背景（玩家标记用一个醒目的底，免得和草丛混在一起）。
func (f *frame) setFgBg(x, y int, ch rune, fg, bg string) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	f.cells[y*f.w+x] = cell{ch: ch, fg: fg, bg: bg}
}

func (f *frame) setBg(x, y int, bg string) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	f.cells[y*f.w+x].bg = bg
}

// put 画一段文本（超出宽度截断；宽字符占两列，第二列标为 cont）。
func (f *frame) put(x, y int, s, fg string) {
	cx := x
	for _, r := range s {
		w := runeWidth(r)
		if cx+w > f.w {
			break
		}
		f.set(cx, y, r, fg)
		if w == 2 {
			f.cells[y*f.w+cx+1] = cell{cont: true, fg: fg}
		}
		cx += w
	}
}

// render 把帧缓冲转成 ANSI 字符串：只在颜色变化时输出转义，减少体积。
func (f *frame) render(noColor bool) string {
	var b strings.Builder
	b.Grow(f.w * f.h * 4)
	var curFg, curBg string
	for y := 0; y < f.h; y++ {
		b.WriteString("\x1b[" + fmt.Sprint(y+1) + ";1H\x1b[K")
		if !noColor {
			curFg, curBg = "", ""
		}
		for x := 0; x < f.w; x++ {
			c := f.cells[y*f.w+x]
			if c.cont {
				continue // 宽字符的第二列由终端自己占掉
			}
			if !noColor {
				if c.fg != curFg {
					b.WriteString(c.fg)
					curFg = c.fg
				}
				if c.bg != curBg {
					if c.bg == "" {
						b.WriteString("\x1b[49m")
					} else {
						b.WriteString(c.bg)
					}
					curBg = c.bg
				}
			}
			b.WriteRune(c.ch)
		}
		if !noColor {
			b.WriteString(cReset)
		}
	}
	return b.String()
}

// ---- 世界 → 帧 ----

// view 描述当前视口：以玩家为中心的格范围。
type view struct {
	x0, y0, w, h int
}

func centerView(cx, cy, w, h int) view {
	return view{x0: cx - w/2, y0: cy - h/2, w: w, h: h}
}

// drawWorld 画地形 + 实体（+ 可选碰撞体叠加）。
func drawWorld(f *frame, w *world, v view, overlay bool) {
	for sy := 0; sy < v.h; sy++ {
		for sx := 0; sx < v.w; sx++ {
			gx, gy := v.x0+sx, v.y0+sy
			if gx < 0 || gy < 0 || gx >= w.width || gy >= w.height {
				f.set(sx, sy, ' ', cGray)
				continue
			}
			ch, fg := terrainCell(w.TileType(gx, gy))
			f.set(sx, sy, ch, fg)
		}
	}
	// 实体按 rank 从低到高画（高 rank 压在上面）。上限用 maxEntityRank，
	// 不能再写死 1..5——自己曾经是 rank 6 却不在这个区间里，于是整个不画，
	// 表现为"我不知道自己在哪"。两层必须一起改。
	for pass := 1; pass <= maxEntityRank; pass++ {
		for _, e := range w.sortedEntities() {
			if e.pos == nil || e.dead != nil {
				continue
			}
			if w.rank(e) != pass {
				continue
			}
			sx, sy := e.tileX()-v.x0, e.tileY()-v.y0
			if sx < 0 || sy < 0 || sx >= v.w || sy >= v.h {
				continue
			}
			ch, fg := entityCell(w, e)
			if e.id == w.own {
				// 玩家标记：亮底黑字（任何地形都是全场最亮的一格）。
				// 相机跟随时"我在哪"全靠它——只靠 @ 和草丛的绿色区分度太低。
				f.setFgBg(sx, sy, ch, "\x1b[30m", ownMarkerBg)
				markSelf(f, sx, sy)
				continue
			}
			f.set(sx, sy, ch, fg)
		}
	}
	// 叠加层最后画：它只改背景色，放在实体之后才能高亮到"实体自己那一格"
	//（先标后画会被实体绘制覆盖掉）。
	if overlay {
		drawCollisionOverlay(f, w, v)
	}
}

// terrainCell 地形 → 字符 + 颜色（与服务端 TerrainType 枚举一一对应）。
func terrainCell(t game.TerrainType) (rune, string) {
	switch t {
	case game.TerrainType_TERRAIN_TYPE_WATER:
		return '~', cBrBlu
	case game.TerrainType_TERRAIN_TYPE_SAND:
		return '.', cBrYel
	case game.TerrainType_TERRAIN_TYPE_ROCK:
		return '^', cGray
	case game.TerrainType_TERRAIN_TYPE_SNOW:
		return '*', cWhite
	case game.TerrainType_TERRAIN_TYPE_GRASS:
		return '"', cGreen
	default:
		return ' ', cGray
	}
}

// entityCell 实体 → 字符 + 颜色。
func entityCell(w *world, e *entity) (rune, string) {
	switch {
	case e.id == w.own:
		return '@', cBold + cBrCyn
	case e.isPlayer():
		return '@', cBrYel
	case e.creature != nil:
		return creatureCell(e.creature.Kind)
	case e.loot != nil:
		return '$', cBrYel
	case e.block != nil:
		// 占位物：树（可砍）/岩石（可挖）/浆果（可采）用不同字符，其余用通用占位符
		if e.hasWork {
			switch e.workAction {
			case game.WorkAction_WORK_ACTION_CHOP:
				return 'T', cBrGrn
			case game.WorkAction_WORK_ACTION_MINE:
				return 'A', cWhite
			default:
				return '*', cBrGrn
			}
		}
		return 'o', cGreen
	case e.station != nil:
		return '&', cMag
	case e.building != nil:
		if e.building.Placed {
			return '#', cBrRed
		}
		return 'n', cGray
	default:
		return '?', cGray
	}
}

func creatureCell(k game.CreatureKind) (rune, string) {
	switch k {
	case game.CreatureKind_CREATURE_KIND_WOLF:
		return 'w', cBrRed
	case game.CreatureKind_CREATURE_KIND_RABBIT:
		return 'r', cWhite
	case game.CreatureKind_CREATURE_KIND_BOAR:
		return 'b', cRed
	case game.CreatureKind_CREATURE_KIND_DEER:
		return 'd', cBrYel
	case game.CreatureKind_CREATURE_KIND_SPIDER:
		return 's', cMag
	default:
		return 'c', cRed
	}
}

// ownMarkerBg 是自己所在格的底色（亮青），overlayBg 是碰撞体叠加层底色（深灰）。
const (
	ownMarkerBg = "\x1b[48;5;51m"
	overlayBg   = "\x1b[48;5;238m"
)

// markSelf 在自己那格周围点四个角标，进一步强化"这是我"（窄字符，不占额外格）。
func markSelf(f *frame, sx, sy int) {
	for _, d := range [][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		x, y := sx+d[0], sy+d[1]
		if x < 0 || y < 0 || x >= f.w || y >= f.h {
			continue
		}
		if c := &f.cells[y*f.w+x]; c.ch == ' ' {
			continue // 不覆盖已有的实体字符
		} else if c.bg == "" {
			c.fg = cBold + cBrCyn
		}
	}
}

// drawCollisionOverlay 把碰撞体压到地形上：
//   - Block：圆按半径标格心周围、盒标占格（这就是服务端形状层的几何）；
//   - DebugShape：有则优先（服务端真正下发的那份，便于验证空间契约）。
//
// 这一层的意义：在终端里就能核对"占位物的碰撞体覆盖了哪几格"，
// 不用开 Godot——树该只挡格心一小块、建筑该占满整格，一眼能看出来。
func drawCollisionOverlay(f *frame, w *world, v view) {
	for _, e := range w.sortedEntities() {
		if e.pos == nil {
			continue
		}
		switch {
		case e.shape != nil:
			markDebugShape(f, v, e)
		case e.block != nil && e.block.Radius > 0:
			// 格心圆：**圆心在占格中心**（anchor+0.5），不是锚点本身。
			// 半径通常 < 0.5，所以按"到格心距离"判定时通常只标中它自己那一格。
			nx, ny := nodeOrigin(e)
			markCircle(f, v, nx, ny, e.block.Radius)
		case e.block != nil:
			markFootprint(f, v, int(e.pos.X), int(e.pos.Y), int(e.block.Width), int(e.block.Height))
		}
	}
}

// nodeOrigin 返回实体节点在格坐标系里的位置——也就是 DebugShape 局部空间的原点。
// 与服务端/客户端的约定一致：Block 实体在占格中心（圆按 1×1 处理），移动体在连续位置。
func nodeOrigin(e *entity) (float64, float64) {
	if e.block != nil {
		bw, bh := float64(e.block.Width), float64(e.block.Height)
		if e.block.Radius > 0 || bw <= 0 || bh <= 0 {
			bw, bh = 1, 1
		}
		return float64(e.pos.X) + bw/2, float64(e.pos.Y) + bh/2
	}
	return e.renderX(), e.renderY()
}

// markFootprint 标出以 (ax,ay) 为左上锚点、bw×bh 的占格（尺寸 ≤ 0 按 1×1）。
func markFootprint(f *frame, v view, ax, ay, bw, bh int) {
	if bw <= 0 {
		bw = 1
	}
	if bh <= 0 {
		bh = 1
	}
	for dy := 0; dy < bh; dy++ {
		for dx := 0; dx < bw; dx++ {
			f.setBg(ax+dx-v.x0, ay+dy-v.y0, overlayBg)
		}
	}
}

// markCircle 标出以 (cx,cy) 为圆心、半径 r 的圆覆盖的格（格心落在圆内即标）。
func markCircle(f *frame, v view, cx, cy, r float64) {
	n := int(r) + 2
	for gy := int(cy) - n; gy <= int(cy)+n; gy++ {
		for gx := int(cx) - n; gx <= int(cx)+n; gx++ {
			if math.Hypot(float64(gx)+0.5-cx, float64(gy)+0.5-cy) <= r {
				f.setBg(gx-v.x0, gy-v.y0, overlayBg)
			}
		}
	}
}

// markDebugShape 把服务端下发的调试形状（节点局部空间）投影到 XZ 平面标格。
func markDebugShape(f *frame, v view, e *entity) {
	s := e.shape
	nx, ny := nodeOrigin(e)
	if s.Kind == game.DebugShape_DEBUG_SHAPE_KIND_BOX {
		bw, bh := int(s.Width+0.5), int(s.Depth+0.5)
		if bw <= 0 {
			bw = 1
		}
		if bh <= 0 {
			bh = 1
		}
		// 盒以节点为中心 → 锚点 = 节点 - 尺寸/2
		markFootprint(f, v, int(nx)-bw/2, int(ny)-bh/2, bw, bh)
		return
	}
	ax, ay := nx+s.AX, ny+s.AZ
	bx, by := nx+s.BX, ny+s.BZ
	if math.Abs(ax-bx) < 1e-6 && math.Abs(ay-by) < 1e-6 {
		markCircle(f, v, ax, ay, s.Radius)
		return
	}
	markCapsule(f, v, ax, ay, bx, by, s.Radius)
}

// markCapsule 标出胶囊（水平段 + 半径）覆盖的格：逐格取格心到线段的距离。
func markCapsule(f *frame, v view, ax, ay, bx, by, r float64) {
	minX := int(math.Min(ax, bx)) - int(r) - 1
	maxX := int(math.Max(ax, bx)) + int(r) + 1
	minY := int(math.Min(ay, by)) - int(r) - 1
	maxY := int(math.Max(ay, by)) + int(r) + 1
	for gy := minY; gy <= maxY; gy++ {
		for gx := minX; gx <= maxX; gx++ {
			if distPointSegment(float64(gx)+0.5, float64(gy)+0.5, ax, ay, bx, by) <= r {
				f.setBg(gx-v.x0, gy-v.y0, overlayBg)
			}
		}
	}
}

// distPointSegment 点 (px,py) 到线段 ab 的距离。
func distPointSegment(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	lenSq := dx*dx + dy*dy
	if lenSq < 1e-12 {
		return math.Hypot(px-ax, py-ay)
	}
	t := ((px-ax)*dx + (py-ay)*dy) / lenSq
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}
