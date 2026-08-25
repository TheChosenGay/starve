package worldmap

import "math"

// 与客户端 Starve.Core.SlopeMesh / IsoMath / SlopeSpeed 对齐。
const (
	IsoStep          = 20.0
	CliffThreshold   = 1.0
	CliffBand        = 0.22
	SlopeFactorMin   = 0.35
	SlopeFactorMax   = 1.0
	terrainTypeWater = 1
)

// CornerHeight 角高度；越界或无数据返回 0。
func (m *MapData) CornerHeight(cx, cy int) float64 {
	if m == nil || cx < 0 || cy < 0 || cx > m.Width || cy > m.Height {
		return 0
	}
	cw := m.Width + 1
	if len(m.CornerHeights) != cw*(m.Height+1) {
		return 0
	}
	return float64(m.CornerHeights[cy*cw+cx])
}

// CornerType 角地形类型；越界或无数据返回 0。
func (m *MapData) CornerType(cx, cy int) int {
	if m == nil || cx < 0 || cy < 0 || cx > m.Width || cy > m.Height {
		return 0
	}
	cw := m.Width + 1
	if len(m.CornerTypes) != cw*(m.Height+1) {
		return 0
	}
	return int(m.CornerTypes[cy*cw+cx])
}

// EdgeHeight 沿一条边的高度轮廓：高差 ≥ 1 时把落差收进低侧 CliffBand。
func EdgeHeight(h0, h1, t float64) float64 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	dh := h1 - h0
	if math.Abs(dh) < CliffThreshold-1e-3 {
		return h0 + dh*t
	}
	if dh < 0 {
		start := 1 - CliffBand
		if t <= start {
			return h0
		}
		return h0 + dh*((t-start)/CliffBand)
	}
	if t >= CliffBand {
		return h1
	}
	return h0 + dh*(t/CliffBand)
}

// HeightOnTile 一格内部高度（平台 + 崖壁带）。
func HeightOnTile(h00, h10, h01, h11, fx, fy float64) float64 {
	hN := EdgeHeight(h00, h10, fx)
	hS := EdgeHeight(h01, h11, fx)
	return EdgeHeight(hN, hS, fy)
}

// HeightAt 世界坐标地面高度，与客户端 TileMap.HeightAt 同一公式。
func (m *MapData) HeightAt(wx, wy float64) float64 {
	if m == nil || m.Width <= 0 || m.Height <= 0 {
		return 0
	}
	x0 := int(math.Floor(wx))
	y0 := int(math.Floor(wy))
	if x0 < 0 {
		x0, wx = 0, 0
	} else if x0 >= m.Width {
		x0, wx = m.Width-1, float64(m.Width)
	}
	if y0 < 0 {
		y0, wy = 0, 0
	} else if y0 >= m.Height {
		y0, wy = m.Height-1, float64(m.Height)
	}
	fx := wx - float64(x0)
	fy := wy - float64(y0)
	h00 := m.CornerHeight(x0, y0)
	h10 := m.CornerHeight(x0+1, y0)
	h01 := m.CornerHeight(x0, y0+1)
	h11 := m.CornerHeight(x0+1, y0+1)
	if waterCorners(m, x0, y0) >= 3 {
		return math.Max(math.Max(h00, h10), math.Max(h01, h11))
	}
	return HeightOnTile(h00, h10, h01, h11, fx, fy)
}

func waterCorners(m *MapData, cx, cy int) int {
	n := 0
	if m.CornerType(cx, cy) == terrainTypeWater {
		n++
	}
	if m.CornerType(cx+1, cy) == terrainTypeWater {
		n++
	}
	if m.CornerType(cx+1, cy+1) == terrainTypeWater {
		n++
	}
	if m.CornerType(cx, cy+1) == terrainTypeWater {
		n++
	}
	return n
}

// ProjectIso 未旋转、未缩放的等距投影，与客户端 IsoMath.WorldToLocal 一致。
func ProjectIso(wx, wy, height float64) (lx, ly float64) {
	return (wx - wy) * IsoStep, (wx+wy)*(IsoStep/2) - height*IsoStep
}

// SlopeFactor 沿当前格子方向走一步的投影边长比，压到 [0.35, 1]。
// 只补偿下坡被投影拉长；上坡不加速。dir 为 (0,0) 或无高度场时为 1。
func SlopeFactor(m *MapData, wx, wy float64, dx, dy int) float64 {
	if dx == 0 && dy == 0 {
		return 1
	}
	return SlopeFactorAt(wx, wy, dx, dy, m.HeightAt)
}

// SlopeFactorAt 用任意高度采样计算因子（golden / 客户端同公式）。
func SlopeFactorAt(wx, wy float64, dx, dy int, heightAt func(wx, wy float64) float64) float64 {
	if dx == 0 && dy == 0 {
		return 1
	}
	if heightAt == nil {
		return 1
	}
	h0 := heightAt(wx, wy)
	h1 := heightAt(wx+float64(dx), wy+float64(dy))
	x0, y0 := ProjectIso(wx, wy, h0)
	xf, yf := ProjectIso(wx+float64(dx), wy+float64(dy), h0)
	xs, ys := ProjectIso(wx+float64(dx), wy+float64(dy), h1)
	flat := math.Hypot(xf-x0, yf-y0)
	sloped := math.Hypot(xs-x0, ys-y0)
	if sloped < 1e-6 {
		return 1
	}
	f := flat / sloped
	if f > SlopeFactorMax {
		return SlopeFactorMax
	}
	if f < SlopeFactorMin {
		return SlopeFactorMin
	}
	return f
}
