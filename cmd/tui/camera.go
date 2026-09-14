package main

// camMode 相机跟随方式。
type camMode int

const (
	// camDeadZone 死区跟随：玩家在中央区域里自由移动时视口**不动**，
	// 走出中央区域才滚动——"是我在动"，像 roguelike。
	camDeadZone camMode = iota
	// camCentered 居中跟随：视口永远以玩家为中心，玩家钉在屏幕正中。
	// 好处是周围视野永远对称，代价是"整个世界在滚"，容易分不清是谁在动。
	camCentered
)

func (m camMode) String() string {
	if m == camCentered {
		return "居中"
	}
	return "死区"
}

// camera 是视口左上角（格）与跟随方式。
type camera struct {
	x0, y0 int
	mode   camMode
	ready  bool
}

// follow 按当前模式把视口挪到合适位置。
func (c *camera) follow(px, py, vw, vh int) {
	if vw <= 0 || vh <= 0 {
		return
	}
	if !c.ready || c.mode == camCentered {
		c.x0, c.y0 = px-vw/2, py-vh/2
		c.ready = true
		return
	}
	// 死区 = 中央 1/3（至少 1 格）：视口只在玩家越出这个矩形时才挪，且只挪刚好够的量，
	// 所以小幅走动时画面完全静止，玩家自己在格子里动。
	dw, dh := vw/3, vh/3
	if dw < 1 {
		dw = 1
	}
	if dh < 1 {
		dh = 1
	}
	if px < c.x0+dw {
		c.x0 = px - dw
	} else if px > c.x0+vw-dw-1 {
		c.x0 = px - (vw - dw - 1)
	}
	if py < c.y0+dh {
		c.y0 = py - dh
	} else if py > c.y0+vh-dh-1 {
		c.y0 = py - (vh - dh - 1)
	}
}

// view 当前视口（供渲染用）。
func (c *camera) view(vw, vh int) view {
	return view{x0: c.x0, y0: c.y0, w: vw, h: vh}
}

// toggle 在两种跟随方式之间切换。
func (c *camera) toggle() {
	if c.mode == camDeadZone {
		c.mode = camCentered
		return
	}
	c.mode = camDeadZone
}
