// movecorpus 生成"跨端一致性语料"（testdata/move_corpus.jsonl）。
//
// 为什么要有它：两端（Go 服务端 / C# 客户端）的移动都是同一个**确定性纯函数**
// ——下一状态 = step(状态, 操作, dt, 世界输入)。于是"两端一致"本质是函数相等，
// 而语料把它变成可判定的事实：一组固定的「场景输入 → 期望位移」，两端各自跑自己的
// 实现去比。任何分歧（运算顺序、float32/float64、0.999 钳制边界、多接触先解谁、
// ORCA 退化与并列打破）都会在 CI 里当红，而不是等玩家走两步感觉卡。
//
// 两条硬约束：
//  1. 期望值必须由**服务端真实求解路径**产出（MoveSystem + 生产同款 solver），
//     不许在这里复刻公式；否则语料就失去权威性。
//  2. 采样必须**偏边界**：均匀随机几乎触发不到分支交界，而分歧恰恰都藏在交界上。
//
// 用法：
//
//	go run ./cmd/movecorpus -seed 11 -count 400 -out testdata/move_corpus.jsonl
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"sort"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
	"starve/internal/game/systems"
	"starve/internal/game/worldmap"
	game "starve/pkg/proto/game"
)

// 语料世界固定 24×24：够放坡/墙/形状/邻居，又小到生成与回放都很快。
const worldSize = 24

type shape struct {
	Kind  string  `json:"kind"` // circle | box | capsule
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	R     float64 `json:"r,omitempty"`
	W     float64 `json:"w,omitempty"`
	H     float64 `json:"h,omitempty"`
	Half  float64 `json:"half,omitempty"`
	FaceX float64 `json:"face_x,omitempty"`
	FaceY float64 `json:"face_y,omitempty"`
}

type neighbor struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	VX         float64 `json:"vx"`
	VY         float64 `json:"vy"`
	Radius     float64 `json:"radius"`
	HalfLength float64 `json:"half_length,omitempty"`
	MaxSpeed   float64 `json:"max_speed"`
}

// heights 线性高度场 h(x,y) = base + grad_x*x + grad_y*y。
//
// 【硬性约束】整张地图的角点高度必须是 [0,255] 内的整数。服务端把角点高度存进
// worldmap.MapData.CornerHeights []byte，越界会按低 8 位回绕（-1→255、-2→254…），
// 于是"真实求解用的高度场"≠"JSON 里声明的线性场"，客户端拿声明值无论如何都复现不了
// （历史上 slope_004/slope_015 两条就是这么红的）。用 newRamp 构造可保证落在值域内，
// step 里的 assertHeightFieldFits 做兜底硬校验。
//
// 另外：服务端/客户端真正用于坡度的是崖壁带采样（|Δh|≥1 时把落差压进 CliffBand=0.22），
// 它**不是**双线性。所以回放必须走生产采样路径（客户端 TileMap.LogicalHeightAt /
// 服务端 worldmap.HeightAt），不能自己拿线性公式求值。
type heights struct {
	Base  float64 `json:"base"`
	GradX float64 `json:"grad_x"`
	GradY float64 `json:"grad_y"`
}

func (h heights) at(x, y float64) float64 { return h.Base + h.GradX*x + h.GradY*y }

// newRamp 构造整数 base 的线性高度场，使整张地图（角点 0..worldSize）的高度都落在
// [0,255]：跨度 span=(|gx|+|gy|)*worldSize ≤ 4*24=96，取 base=span+16 ⇒ 高度 ∈ [16,208]。
// gx/gy 非整数或超出该范围时 assertHeightFieldFits 会直接报错，不会静默生成废语料。
func newRamp(gx, gy float64) heights {
	span := (math.Abs(gx) + math.Abs(gy)) * float64(worldSize)
	return heights{Base: math.Ceil(span) + 16, GradX: gx, GradY: gy}
}

// assertHeightFieldFits 硬校验：角点高度必须是 [0,255] 整数，否则 byte 存不下 ⇒ 语料失效。
func assertHeightFieldFits(h heights) {
	for y := 0; y <= worldSize; y++ {
		for x := 0; x <= worldSize; x++ {
			v := h.at(float64(x), float64(y))
			if v != math.Trunc(v) || v < 0 || v > 255 {
				panic(fmt.Sprintf(
					"语料高度场越界：h(%d,%d)=%v，角点高度必须是 [0,255] 整数；"+
						"否则 []byte 回绕，服务端真实高度场与 JSON 声明不符，客户端无法复现",
					x, y, v))
			}
		}
	}
}

type scenario struct {
	Name       string     `json:"name"`
	Category   string     `json:"category"`
	StartX     float64    `json:"start_x"`
	StartY     float64    `json:"start_y"`
	DX         int        `json:"dx"`
	DY         int        `json:"dy"`
	Speed      float64    `json:"speed"`
	DTMS       float64    `json:"dt_ms"`
	Blocked    [][2]int   `json:"blocked,omitempty"`
	Heights    *heights   `json:"heights,omitempty"`
	Shapes     []shape    `json:"shapes,omitempty"`
	BodyRadius float64    `json:"body_radius,omitempty"`
	Neighbors  []neighbor `json:"neighbors,omitempty"`
	WantX      float64    `json:"want_x"`
	WantY      float64    `json:"want_y"`
}

type meta struct {
	Seed  int64 `json:"seed"`
	Count int   `json:"count"`
}

func main() {
	seed := flag.Int64("seed", 11, "随机种子（同一 seed 必须产出完全相同的语料）")
	count := flag.Int("count", 400, "场景条数")
	out := flag.String("out", "testdata/move_corpus.jsonl", "输出路径（JSONL）")
	flag.Parse()

	scenarios := build(*seed, *count)

	fh, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建输出失败:", err)
		os.Exit(1)
	}
	defer fh.Close()
	enc := json.NewEncoder(fh)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(map[string]meta{"meta": {Seed: *seed, Count: len(scenarios)}}); err != nil {
		fmt.Fprintln(os.Stderr, "写元信息失败:", err)
		os.Exit(1)
	}
	byCat := map[string]int{}
	for _, s := range scenarios {
		if err := enc.Encode(s); err != nil {
			fmt.Fprintln(os.Stderr, "写场景失败:", err)
			os.Exit(1)
		}
		byCat[s.Category]++
	}
	cats := make([]string, 0, len(byCat))
	for c := range byCat {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	fmt.Printf("seed=%d 条数=%d → %s\n", *seed, len(scenarios), *out)
	for _, c := range cats {
		fmt.Printf("  %-10s %d\n", c, byCat[c])
	}
}

func build(seed int64, count int) []scenario {
	rng := rand.New(rand.NewSource(seed))
	var out []scenario
	add := func(s scenario) {
		s.WantX, s.WantY = step(s)
		out = append(out, s)
	}

	perCat := count / 9
	for i := 0; i < perCat; i++ {
		add(baseScenario(rng, i))
	}
	for i := 0; i < perCat; i++ {
		add(diagonalScenario(rng, i))
	}
	for i := 0; i < perCat; i++ {
		add(slopeScenario(rng, i))
	}
	for i := 0; i < perCat; i++ {
		add(wallScenario(rng, i))
	}
	for i := 0; i < perCat; i++ {
		add(circleScenario(rng, i))
	}
	for i := 0; i < perCat; i++ {
		add(boxScenario(rng, i))
	}
	for i := 0; i < perCat; i++ {
		add(capsuleScenario(rng, i))
	}
	for i := 0; i < perCat; i++ {
		add(orcaScenario(rng, i))
	}
	for i := 0; i < perCat+count%9; i++ {
		add(comboScenario(rng, i))
	}
	return out
}

// ── 场景构造 ────────────────────────────────────────────────

func baseScenario(rng *rand.Rand, i int) scenario {
	dirs := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	d := dirs[rng.Intn(len(dirs))]
	dt := []float64{50, 50, 50, 16, 33}[rng.Intn(5)]
	return scenario{
		Name: fmt.Sprintf("base_%03d", i), Category: "base",
		StartX: 8 + rng.Float64()*8, StartY: 8 + rng.Float64()*8,
		DX: d[0], DY: d[1],
		Speed: 6 + rng.Float64()*8, DTMS: dt, BodyRadius: 0.3,
	}
}

// 对角：dx、dy 同时非零 ⇒ 必须 `1/√2` 归一化（客户端最容易漏的一步）。
func diagonalScenario(rng *rand.Rand, i int) scenario {
	dirs := [4][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
	d := dirs[rng.Intn(len(dirs))]
	return scenario{
		Name: fmt.Sprintf("diag_%03d", i), Category: "diagonal",
		StartX: 8 + rng.Float64()*6, StartY: 8 + rng.Float64()*6,
		DX: d[0], DY: d[1],
		Speed: 6 + rng.Float64()*8, DTMS: 50, BodyRadius: 0.3,
	}
}

// 坡度：上坡/下坡/侧坡 + 陡坡（触发 SlopeFactorMin/Max 钳制）。
func slopeScenario(rng *rand.Rand, i int) scenario {
	grads := []float64{-2, -2, -1, -1, 0, 0, 1, 1, 2, 2}
	gx, gy := grads[rng.Intn(len(grads))], grads[rng.Intn(len(grads))]
	if gx == 0 && gy == 0 {
		gx = 1
	}
	h := newRamp(gx, gy)
	dirs := [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	d := dirs[rng.Intn(len(dirs))]
	return scenario{
		Name: fmt.Sprintf("slope_%03d", i), Category: "slope",
		StartX: 6 + rng.Float64()*10, StartY: 6 + rng.Float64()*10,
		DX: d[0], DY: d[1],
		Speed: 8 + rng.Float64()*6, DTMS: 50, Heights: &h, BodyRadius: 0.3,
	}
}

// 墙边界钳制：正方向停在 0.999、负方向停在 0.001（服务端 stepAxis 的边界约定）。
func wallScenario(rng *rand.Rand, i int) scenario {
	x := 10 + rng.Intn(4)
	y := 10 + rng.Intn(4)
	switch i % 4 {
	case 0: // +X 撞墙（sub 恰好贴边）
		return scenario{
			Name: fmt.Sprintf("wall_pos_%03d", i), Category: "wall",
			StartX: float64(x) + 0.995, StartY: float64(y), DX: 1, DY: 0,
			Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Blocked: [][2]int{{x + 1, y}},
		}
	case 1: // -X 撞墙
		return scenario{
			Name: fmt.Sprintf("wall_neg_%03d", i), Category: "wall",
			StartX: float64(x) + 0.005, StartY: float64(y), DX: -1, DY: 0,
			Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Blocked: [][2]int{{x - 1, y}},
		}
	case 2: // 本 tick 会跨格但目标不可走 ⇒ 必须停在边界而不是穿过去
		return scenario{
			Name: fmt.Sprintf("wall_cross_%03d", i), Category: "wall",
			StartX: float64(x) + 0.6, StartY: float64(y), DX: 1, DY: 0,
			Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Blocked: [][2]int{{x + 1, y}},
		}
	default: // 贴墙 + 侧向：一轴被挡、另一轴滑动
		return scenario{
			Name: fmt.Sprintf("wall_slide_%03d", i), Category: "wall",
			StartX: float64(x) + 0.99, StartY: float64(y) + 0.4, DX: 1, DY: 1,
			Speed: 8, DTMS: 50, BodyRadius: 0.3,
			Blocked: [][2]int{{x + 1, y}},
		}
	}
}

// 圆（树/岩）：正撞停住、偏移擦过、恰好相切（半径和 ≈ 距离）。
func circleScenario(rng *rand.Rand, i int) scenario {
	x := 12 + rng.Float64()*4
	y := 12 + rng.Float64()*4
	r := 0.2 + rng.Float64()*0.4
	body := 0.3
	dist := body + r
	offset := []float64{0, 0, 0.05, r + 0.1, -(r + 0.1)}[i%5]
	return scenario{
		Name: fmt.Sprintf("circle_%03d", i), Category: "circle",
		StartX: x, StartY: y, DX: 1, DY: 0,
		Speed: 8 + rng.Float64()*4, DTMS: 50, BodyRadius: body,
		Shapes: []shape{{Kind: "circle", X: x + dist, Y: y + offset, R: r}},
	}
}

// 盒（建筑）：正面停、斜着沿面滑、从角点擦过（考验角点辅助滑动）。
func boxScenario(rng *rand.Rand, i int) scenario {
	x := 12 + rng.Float64()*3
	y := 12 + rng.Float64()*3
	w, h := 2.0, 2.0
	switch i % 3 {
	case 0:
		return scenario{
			Name: fmt.Sprintf("box_face_%03d", i), Category: "box",
			StartX: x, StartY: y + 0.5, DX: 1, DY: 0,
			Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Shapes: []shape{{Kind: "box", X: x + 1, Y: y - 0.5, W: w, H: h}},
		}
	case 1:
		return scenario{
			Name: fmt.Sprintf("box_slide_%03d", i), Category: "box",
			StartX: x, StartY: y - 0.2, DX: 1, DY: 1,
			Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Shapes: []shape{{Kind: "box", X: x + 1, Y: y - 1, W: w, H: h}},
		}
	default:
		return scenario{
			Name: fmt.Sprintf("box_corner_%03d", i), Category: "box",
			StartX: x, StartY: y - 1.2, DX: 1, DY: 1,
			Speed: 12, DTMS: 50, BodyRadius: 0.3,
			Shapes: []shape{{Kind: "box", X: x + 1, Y: y, W: w, H: h}},
		}
	}
}

// 胶囊（会动的身体）：**服务端的静态碰撞索引只有 circle/box**，胶囊只作为"动态体"
// （Player/动物）存在。所以这一类用"零速度的邻居"表达一个**静止的移动体**：
// 两边都会把它当动态胶囊走 ORCA 路径，这才是生产里真实存在的那条路径。
//
// （顺带记录一个已知差异：客户端 MovementSlide 把喂进来的胶囊当**硬障碍**，而服务端
// 动态体只走 ORCA 软避让 —— 所以生产接线里客户端**只喂静态形状**，不喂胶囊；
// 语料同样不喂静态胶囊，否则测的是一条生产不存在的路径。）
func capsuleScenario(rng *rand.Rand, i int) scenario {
	x := 12 + rng.Float64()*3
	y := 12 + rng.Float64()*3
	r, half := 0.3, 0.5
	switch i % 3 {
	case 0: // 正前方静止：被软避让推开
		return scenario{
			Name: fmt.Sprintf("capsule_headon_%03d", i), Category: "capsule",
			StartX: x, StartY: y, DX: 1, DY: 0,
			Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Neighbors: []neighbor{{X: x + 1.1, Y: y, VX: 0, VY: 0, Radius: r, HalfLength: half, MaxSpeed: 10}},
		}
	case 1: // 侧面擦过（带偏移）
		return scenario{
			Name: fmt.Sprintf("capsule_offset_%03d", i), Category: "capsule",
			StartX: x, StartY: y, DX: 1, DY: 0,
			Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Neighbors: []neighbor{{X: x + 1.0, Y: y + r + 0.32, VX: 0, VY: 0, Radius: r, HalfLength: half, MaxSpeed: 10}},
		}
	default: // 斜向穿过一个静止体
		return scenario{
			Name: fmt.Sprintf("capsule_along_%03d", i), Category: "capsule",
			StartX: x, StartY: y - 0.9, DX: 1, DY: 1,
			Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Neighbors: []neighbor{{X: x + 1.0, Y: y - 1.6, VX: 0, VY: 0, Radius: r, HalfLength: half, MaxSpeed: 10}},
		}
	}
}

// ORCA：迎面、追尾、并排、三体挤压。
//
// ⚠️ 这里**刻意不做完全对称的正面对撞**：服务端生产 solver 用 NewORCASolver
// （breakSymmetry=false），而客户端 OwnMovePredictor 用 breakSymmetry=true + 实体 id。
// 完全共线时两者会选不同侧 ⇒ 必然不一致。这是已发现的待修一致性问题（见
// docs/P1.4），修完再补对称场景；现在所有 ORCA 场景都带一点横向偏移，
// 这样两侧都会走"非退化"分支，不受对称打破开关影响。
func orcaScenario(rng *rand.Rand, i int) scenario {
	x := 12 + rng.Float64()*2
	y := 12 + rng.Float64()*2
	skew := 0.05 + rng.Float64()*0.05 // 刻意偏移，避开对称退化
	switch i % 4 {
	case 0: // 迎面（带 skew）
		return scenario{
			Name: fmt.Sprintf("orca_headon_%03d", i), Category: "orca",
			StartX: x, StartY: y, DX: 1, DY: 0, Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Neighbors: []neighbor{{X: x + 1.2, Y: y + skew, VX: -10, VY: 0, Radius: 0.3, MaxSpeed: 10}},
		}
	case 1: // 追尾（同向，前者慢）
		return scenario{
			Name: fmt.Sprintf("orca_follow_%03d", i), Category: "orca",
			StartX: x, StartY: y, DX: 1, DY: 0, Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Neighbors: []neighbor{{X: x + 1.0, Y: y + skew, VX: 4, VY: 0, Radius: 0.3, MaxSpeed: 4}},
		}
	case 2: // 并排同向（横向挤压）
		return scenario{
			Name: fmt.Sprintf("orca_side_%03d", i), Category: "orca",
			StartX: x, StartY: y, DX: 1, DY: 0, Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Neighbors: []neighbor{{X: x + 0.3, Y: y + 0.62, VX: 10, VY: 0, Radius: 0.3, MaxSpeed: 10}},
		}
	default: // 三体挤压（两侧各一个，前方一个）
		return scenario{
			Name: fmt.Sprintf("orca_three_%03d", i), Category: "orca",
			StartX: x, StartY: y, DX: 1, DY: 0, Speed: 10, DTMS: 50, BodyRadius: 0.3,
			Neighbors: []neighbor{
				{X: x + 1.1, Y: y + skew, VX: -8, VY: 0, Radius: 0.3, MaxSpeed: 8},
				{X: x + 0.2, Y: y - 0.6, VX: 6, VY: 2, Radius: 0.3, MaxSpeed: 8},
				{X: x - 0.2, Y: y + 0.6, VX: 4, VY: -2, Radius: 0.3, MaxSpeed: 8},
			},
		}
	}
}

// 组合：坡 + 形状 + 邻居同时出现（真实场景里三者从不单独出现）。
func comboScenario(rng *rand.Rand, i int) scenario {
	x := 12 + rng.Float64()*3
	y := 12 + rng.Float64()*3
	h := newRamp([]float64{-1, 0, 1}[i%3], []float64{1, -1, 1}[(i/3)%3])
	return scenario{
		Name: fmt.Sprintf("combo_%03d", i), Category: "combo",
		StartX: x, StartY: y, DX: 1, DY: 1, Speed: 10, DTMS: 50,
		Heights: &h, BodyRadius: 0.3,
		Shapes:    []shape{{Kind: "circle", X: x + 1.6, Y: y + 1.4, R: 0.35}},
		Neighbors: []neighbor{{X: x + 1.0, Y: y + 0.55, VX: -6, VY: -6, Radius: 0.3, MaxSpeed: 10}},
	}
}

// ── 真实求解（生成期望值）─────────────────────────────────────

func step(s scenario) (float64, float64) {
	sim := ecs.NewWorld()
	md := &worldmap.MapData{
		Width: worldSize, Height: worldSize,
		CornerTypes:   make([]byte, (worldSize+1)*(worldSize+1)),
		CornerHeights: make([]byte, (worldSize+1)*(worldSize+1)),
	}
	if s.Heights != nil {
		// 兜底硬校验：值域外的角点在下面的 byte() 处会回绕，语料就自相矛盾了。
		assertHeightFieldFits(*s.Heights)
		for y := 0; y <= worldSize; y++ {
			for x := 0; x <= worldSize; x++ {
				md.CornerHeights[y*(worldSize+1)+x] = byte(s.Heights.at(float64(x), float64(y)))
			}
		}
	}
	for _, cell := range s.Blocked {
		md.CornerTypes[cell[1]*(worldSize+1)+cell[0]] = byte(game.TerrainType_TERRAIN_TYPE_WATER)
	}
	sim.AddResource(md)

	idx := collision.NewIndex()
	sim.AddResource(idx)
	for i, sh := range s.Shapes {
		e := ecs.Entity(i + 1)
		if sh.Kind == "box" {
			idx.SetBox(e, sh.X, sh.Y, sh.W, sh.H)
		} else {
			idx.Set(e, sh.X, sh.Y, sh.R)
		}
	}

	// 邻居：Position + Moveable + Collide(capsule)。SyncDynamicBodies 会按"有 Moveable"把它们
	// 注册进动态层/ORCA 邻居表 —— 与真实世界里"另一个玩家/生物"完全同一条路径。
	for _, nb := range s.Neighbors {
		e := sim.CreateEntity()
		nx, ny := int(math.Floor(nb.X)), int(math.Floor(nb.Y))
		ecs.Add(sim, e, components.Position{X: nx, Y: ny})
		ecs.Add(sim, e, components.Moveable{
			Speed: nb.MaxSpeed, EffectiveSpeed: nb.MaxSpeed,
			DirX: axisSign(nb.VX), DirY: axisSign(nb.VY),
			VelX: nb.VX, VelY: nb.VY,
			SubX: nb.X - float64(nx), SubY: nb.Y - float64(ny),
		})
		ecs.Add(sim, e, components.Collide{
			Shape: components.CollideShapeCapsule, Radius: nb.Radius, HalfLength: nb.HalfLength,
		})
	}

	// 被求解的移动体（= 玩家这一步）。
	x, y := int(math.Floor(s.StartX)), int(math.Floor(s.StartY))
	mover := sim.CreateEntity()
	ecs.Add(sim, mover, components.Position{X: x, Y: y})
	ecs.Add(sim, mover, components.Moveable{
		Speed: s.Speed, EffectiveSpeed: s.Speed, DirX: s.DX, DirY: s.DY,
		SubX: s.StartX - float64(x), SubY: s.StartY - float64(y),
	})
	if s.BodyRadius > 0 {
		ecs.Add(sim, mover, components.Collide{
			Shape: components.CollideShapeCapsule, Radius: s.BodyRadius,
		})
	}

	// 生产同款 solver（含 AOI 与默认 ORCA 参数）—— 语料的权威性就来自这一行。
	(&systems.MoveSystem{Solver: systems.NewDefaultMoveSolver()}).
		Update(sim, time.Duration(s.DTMS*float64(time.Millisecond)))

	p := ecs.Get[components.Position](sim, mover)
	mv := ecs.Get[components.Moveable](sim, mover)
	return float64(p.X) + mv.SubX, float64(p.Y) + mv.SubY
}

func axisSign(v float64) int {
	switch {
	case v > 1e-9:
		return 1
	case v < -1e-9:
		return -1
	default:
		return 0
	}
}
