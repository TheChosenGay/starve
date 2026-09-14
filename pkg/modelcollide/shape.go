package modelcollide

import (
	"fmt"
	"math"
	"sort"
)

// Rules 是"模型 → 简化碰撞体"的规则（写在 configs/models.json 清单里，不藏在代码里）。
//
// 为什么要有规则而不是"直接取包围盒"：
//   - 树/灌木的树冠比树干宽得多，碰撞要的是**树干**（穿过树冠是允许的）；
//   - 角色/动物的手臂、鹿角、尾巴是视觉细节，碰撞取躯干；
//   - 美术资产的杂散点（飘带、粒子片）要用百分位裁掉。
//
// 所以：在高度带内量"水平截面"，取**较短那条边的一半**当半径——
// 长条模型（狼/鹿，鼻子到尾巴）的短边是躯干宽度、长边是体长，体长不能进碰撞半径，
// 否则角色会被自己的尾巴顶开。用分位裁掉两侧的离群点（percentile=0.98 → 两端各裁 1%）。
type Rules struct {
	Scale      float64    // 模型单位 → 格（= 客户端 ModelScale 常量）
	Kind       string     // "circle"（格心圆）| "capsule"（圆柱：半径 + 身高）| "box"（占格盒）
	Band       [2]float64 // 取样高度带（0..1，相对模型高度）；零值 = 全高
	Percentile float64    // 保留分位（0..1）；零值 = 0.98（两端各裁 1%）
	// Axis 胶囊轴向："vertical"（直立：人物/树/建筑，段沿 Y）| "body"（四足：段沿水平长轴）。
	// 四足模型的"长"（鼻子到尾巴）必须进胶囊长度而不是半径，否则贴脸站会被空气墙顶开。
	Axis string
}

// CapsuleShape 服务端碰撞体（也是调试渲染用的形状）：段 a→b + 半径，单位=格，
// 坐标相对模型原点（= 服务端格心 + 地面）。
//   - vertical：a=(cx,0,cz)、b=(cx,height,cz)，半径按取样带（躯干/树干）量；
//   - body：段沿水平长轴铺开，半径取短边一半，长度 = 长边 - 2r（长短都刚好包住）。
type CapsuleShape struct {
	Axis   string     `json:"axis"`
	A      [3]float64 `json:"a"`
	B      [3]float64 `json:"b"`
	Radius float64    `json:"radius"`
	Height float64    `json:"height"`
}

// HalfLength 胶囊半长（格）：**水平**段长的一半；直立胶囊（人物/树）返回 0。
// 服务端用它做碰撞体（沿朝向铺开），长宽才刚好包住模型。
func (c CapsuleShape) HalfLength() float64 {
	dx := c.B[0] - c.A[0]
	dz := c.B[2] - c.A[2]
	if c.Axis != "body" {
		return 0
	}
	return round3(math.Sqrt(dx*dx+dz*dz) / 2)
}

// Proxy 是算出来的服务端简化碰撞体（单位=格）。
type Proxy struct {
	Kind       string     `json:"kind"`
	Scale      float64    `json:"scale"`
	Band       [2]float64 `json:"band"`
	Percentile float64    `json:"percentile"`
	Points     int        `json:"points"`    // 取样带内的顶点数
	Radius     float64    `json:"radius"`    // circle / capsule 半径（保留 3 位）
	Height     float64    `json:"height"`    // capsule 身高（模型高度 × scale）
	BoxFloat   [2]float64 `json:"box_float"` // box 精确足迹（宽, 深；格）
	BoxTiles   [2]int     `json:"box_tiles"` // box 向上取整的占格（宽, 高）
	// ExtentX/ExtentZ 取样带水平截面的两条边（格）：短边的一半就是半径，
	// 两条都记下来，方便人核对"是不是取错了轴"。
	ExtentX float64 `json:"extent_x"`
	ExtentZ float64 `json:"extent_z"`
	// 诊断：模型原点与取样带形心的水平偏差（格）。服务端圆以模型原点（= 格心）为轴，
	// 偏差大说明模型没居中，应该让美术重新导出，而不是把半径算大。
	CenterOffset [2]float64    `json:"center_offset"`
	MaxRadius    float64       `json:"max_radius"` // 取样带内离原点最远点（未裁剪，只作诊断）
	Bounds       [3][2]float64 `json:"bounds"`     // 模型包围盒（x/y/z 的 min,max；格）
	Capsule      CapsuleShape  `json:"capsule"`    // 服务端碰撞体 / 调试渲染形状
}

// Derive 按规则从顶点云算出简化碰撞体。
func Derive(m *Mesh, rules Rules) (Proxy, error) {
	scale := rules.Scale
	if scale <= 0 {
		scale = 1
	}
	p := Proxy{Kind: rules.Kind, Scale: scale, Points: 0}
	height := (m.Max.Y - m.Min.Y) * scale
	p.Height = round3(height)
	for axis, minMax := range [3][2]float64{
		{m.Min.X * scale, m.Max.X * scale},
		{m.Min.Y * scale, m.Max.Y * scale},
		{m.Min.Z * scale, m.Max.Z * scale},
	} {
		p.Bounds[axis] = [2]float64{round3(minMax[0]), round3(minMax[1])}
	}

	band := rules.Band
	if band[0] == 0 && band[1] == 0 {
		band = [2]float64{0, 1}
	}
	if band[0] < 0 || band[1] > 1 || band[0] >= band[1] {
		return Proxy{}, fmt.Errorf("非法取样高度带 %v（应在 0..1 且 min<max）", band)
	}
	p.Band = band

	percentile := rules.Percentile
	if percentile <= 0 {
		percentile = 0.98
	}
	if percentile > 1 {
		return Proxy{}, fmt.Errorf("非法百分位 %v（应 ≤ 1）", percentile)
	}
	p.Percentile = percentile

	// 取样带内的点：半径以模型原点（= 服务端格心）为轴心
	spanY := m.Max.Y - m.Min.Y
	xs := make([]float64, 0, len(m.Points))
	zs := make([]float64, 0, len(m.Points))
	centerX, centerZ, maxR := 0.0, 0.0, 0.0
	for _, v := range m.Points {
		t := 0.0
		if spanY > 0 {
			t = (v.Y - m.Min.Y) / spanY
		}
		if t < band[0] || t > band[1] {
			continue
		}
		xs = append(xs, v.X*scale)
		zs = append(zs, v.Z*scale)
		centerX += v.X * scale
		centerZ += v.Z * scale
		if r := math.Hypot(v.X, v.Z) * scale; r > maxR {
			maxR = r
		}
	}
	if len(xs) == 0 {
		return Proxy{}, fmt.Errorf("取样高度带 %v 内没有顶点（检查 band 规则）", band)
	}
	p.Points = len(xs)
	sort.Float64s(xs)
	sort.Float64s(zs)
	// 样本太少时裁剪没有统计意义（几十个顶点的躯干会把 1% 裁成可见误差），
	// 所以只有样本足够时才裁两侧离群点。
	tail := 0.0
	if len(xs) >= minTrimSamples {
		tail = (1 - percentile) / 2
	}
	p.ExtentX = round3(quantileSorted(xs, 1-tail) - quantileSorted(xs, tail))
	p.ExtentZ = round3(quantileSorted(zs, 1-tail) - quantileSorted(zs, tail))
	p.Radius = round3(0.5 * math.Min(p.ExtentX, p.ExtentZ))
	p.MaxRadius = round3(maxR)
	p.CenterOffset = [2]float64{
		round3(centerX / float64(len(xs))),
		round3(centerZ / float64(len(xs))),
	}

	// 盒形足迹取整个模型的水平包围盒（建筑类不受高度带影响）
	p.BoxFloat = [2]float64{
		round3((m.Max.X - m.Min.X) * scale),
		round3((m.Max.Z - m.Min.Z) * scale),
	}
	p.BoxTiles = [2]int{
		int(math.Ceil(p.BoxFloat[0] - 1e-9)),
		int(math.Ceil(p.BoxFloat[1] - 1e-9)),
	}
	if p.BoxTiles[0] < 1 {
		p.BoxTiles[0] = 1
	}
	if p.BoxTiles[1] < 1 {
		p.BoxTiles[1] = 1
	}
	p.Capsule = capsuleOf(m, scale, rules.Axis, band, p.Radius, p.ExtentX, p.ExtentZ)
	return p, nil
}

// capsuleOf 拼出服务端碰撞胶囊（也用于调试渲染）：
//   - 半径按取样带量（手感：只有躯干/树干挡人）；
//   - **长、宽、高按整个模型量**：段从原点（= 服务端格心）向两侧铺开到刚好包住模型。
//     形状描述在模型局部空间（沿 ±轴），客户端按自己的朝向旋转就能对齐。
func capsuleOf(m *Mesh, scale float64, axis string, band [2]float64, radius, extentX, extentZ float64) CapsuleShape {
	baseY := m.Min.Y * scale
	topY := m.Max.Y * scale
	cap := CapsuleShape{
		Axis:   "vertical",
		Radius: radius,
		Height: round3(topY - baseY),
		A:      [3]float64{0, round3(baseY), 0},
		B:      [3]float64{0, round3(topY), 0},
	}
	if axis != "body" {
		return cap
	}
	// 四足：段沿水平长轴（按整个模型的包围盒选轴），铺到刚好包住鼻子和尾巴。
	midY := round3((baseY + topY) / 2)
	fullX := (m.Max.X - m.Min.X) * scale
	fullZ := (m.Max.Z - m.Min.Z) * scale
	if fullX >= fullZ {
		half := math.Max(math.Abs(m.Max.X), math.Abs(m.Min.X))*scale - radius
		half = math.Max(half, 0)
		cap.Axis = "body"
		cap.A = [3]float64{round3(-half), midY, 0}
		cap.B = [3]float64{round3(half), midY, 0}
		return cap
	}
	half := math.Max(math.Abs(m.Max.Z), math.Abs(m.Min.Z))*scale - radius
	half = math.Max(half, 0)
	cap.Axis = "body"
	cap.A = [3]float64{0, midY, round3(-half)}
	cap.B = [3]float64{0, midY, round3(half)}
	return cap
}

// quantileSorted 线性插值分位（输入必须已升序；确定性）。
func quantileSorted(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[n-1]
	}
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	if lo+1 >= n {
		return sorted[n-1]
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[lo+1]*frac
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// minTrimSamples 是启用分位裁剪所需的最小样本数。
const minTrimSamples = 100
