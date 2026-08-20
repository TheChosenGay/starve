package systems

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

type movementGolden struct {
	Name    string   `json:"name"`
	StartX  float64  `json:"start_x"`
	StartY  float64  `json:"start_y"`
	DX      int      `json:"dx"`
	DY      int      `json:"dy"`
	Speed   float64  `json:"speed"`
	DTMS    float64  `json:"dt_ms"`
	Blocked [][2]int `json:"blocked"`
	WantX   float64  `json:"want_x"`
	WantY   float64  `json:"want_y"`
}

func TestMovementGoldenVectors(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/movement_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []movementGolden
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			blocked := make(map[[2]int]bool, len(vector.Blocked))
			for _, cell := range vector.Blocked {
				blocked[cell] = true
			}
			x := int(math.Floor(vector.StartX))
			y := int(math.Floor(vector.StartY))
			subX := vector.StartX - float64(x)
			subY := vector.StartY - float64(y)
			dist := vector.Speed * vector.DTMS / 1000
			if vector.DX != 0 && vector.DY != 0 {
				dist /= math.Sqrt2
			}
			if vector.DX != 0 {
				x, subX, _ = stepAxis(x, subX, vector.DX, dist, func(nextX int) bool {
					return !blocked[[2]int{nextX, y}]
				})
			}
			if vector.DY != 0 {
				y, subY, _ = stepAxis(y, subY, vector.DY, dist, func(nextY int) bool {
					return !blocked[[2]int{x, nextY}]
				})
			}
			if got := float64(x) + subX; math.Abs(got-vector.WantX) > 1e-6 {
				t.Fatalf("x = %.10f, want %.10f", got, vector.WantX)
			}
			if got := float64(y) + subY; math.Abs(got-vector.WantY) > 1e-6 {
				t.Fatalf("y = %.10f, want %.10f", got, vector.WantY)
			}
		})
	}
}
