package systems

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
	game "starve/pkg/proto/game"
)

// goldenVector 是一条"一个 tick 的权威位移"金标准向量。
// 客户端 OwnMovementSim 必须实现同一套公式（坡度因子 + 形状层扫掠滑动 + stepAxis），
// 否则贴近树/墙边会被快照反复校正。
type goldenVector struct {
	Name   string  `json:"name"`
	StartX float64 `json:"start_x"`
	StartY float64 `json:"start_y"`
	DX     int     `json:"dx"`
	DY     int     `json:"dy"`
	Speed  float64 `json:"speed"`
	DTMS   float64 `json:"dt_ms"`
	// Blocked 硬墙格（地形：水/悬崖）。占位物不是硬墙——它们走 Shapes。
	Blocked [][2]int      `json:"blocked"`
	Shapes  []goldenShape `json:"shapes"`
	// BodyRadius 移动体碰撞半径（格）；有 Solids 时必须给。
	BodyRadius float64 `json:"body_radius"`
	// Tol 比较容差；默认 1e-6，带接触回退（skin≈1e-3）的向量给 2e-3。
	Tol   float64 `json:"tol"`
	WantX float64 `json:"want_x"`
	WantY float64 `json:"want_y"`
}

// goldenShape 占位物形状：kind=circle 用 x/y/r（格心圆，树干/岩石），
// kind=box 用 x/y/w/h（左上角锚点 + 占格尺寸，建筑/工作站）。
type goldenShape struct {
	Kind string  `json:"kind"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	R    float64 `json:"r"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

// TestMovementGoldenVectors 用金标准向量锁定一个 tick 的位移。
// 向量在最小世界里跑真实 MoveSystem（不是复刻公式），所以服务端改公式时这里会红。
func TestMovementGoldenVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/movement_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []goldenVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			x, y := stepGoldenVector(vector)
			tol := vector.Tol
			if tol <= 0 {
				tol = 1e-6
			}
			if math.Abs(x-vector.WantX) > tol {
				t.Fatalf("x = %.10f, want %.10f (±%v)", x, vector.WantX, tol)
			}
			if math.Abs(y-vector.WantY) > tol {
				t.Fatalf("y = %.10f, want %.10f (±%v)", y, vector.WantY, tol)
			}
		})
	}
}

// stepGoldenVector 建一个最小世界（平地 + 给定阻挡格 + 给定碰撞体），跑一次 MoveSystem。
func stepGoldenVector(vector goldenVector) (float64, float64) {
	sim := ecs.NewWorld()
	md := &worldmap.MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)}
	sim.AddResource(md)
	for _, cell := range vector.Blocked {
		md.CornerTypes[cell[1]*17+cell[0]] = byte(game.TerrainType_TERRAIN_TYPE_WATER) // 硬墙 = 地形水
	}
	cw := collision.NewIndex()
	for i, s := range vector.Shapes {
		e := ecs.Entity(i + 1)
		if s.Kind == "box" {
			cw.SetBox(e, s.X, s.Y, s.W, s.H)
			continue
		}
		cw.Set(e, s.X, s.Y, s.R)
	}
	sim.AddResource(cw)

	x := int(math.Floor(vector.StartX))
	y := int(math.Floor(vector.StartY))
	e := sim.CreateEntity()
	ecs.Add(sim, e, components.Position{X: x, Y: y})
	ecs.Add(sim, e, components.Moveable{
		Speed: vector.Speed,
		DirX:  vector.DX,
		DirY:  vector.DY,
		SubX:  vector.StartX - float64(x),
		SubY:  vector.StartY - float64(y),
	})
	// 向量自带的移动体半径（客户端预测必须用同一个值，不能只靠全局缺省）：
	// 现在挂在独立的 Collide 组件上，动态实体还需要 Dynamic 标记。
	if vector.BodyRadius > 0 {
		ecs.Add(sim, e, components.Collide{
			Shape:  components.CollideShapeCapsule,
			Radius: vector.BodyRadius,
		})
	}
	(&MoveSystem{}).Update(sim, time.Duration(vector.DTMS*float64(time.Millisecond)))
	p := ecs.Get[components.Position](sim, e)
	mv := ecs.Get[components.Moveable](sim, e)
	return float64(p.X) + mv.SubX, float64(p.Y) + mv.SubY
}
