package worldmap

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

func TestHeightAtTerraceThenCliff(t *testing.T) {
	md := southDropMap()
	if got := md.HeightAt(0.5, 0.1); math.Abs(got-1) > 1e-6 {
		t.Fatalf("terrace = %v, want 1", got)
	}
	if got := md.HeightAt(0.5, 1-CliffBand); math.Abs(got-1) > 1e-6 {
		t.Fatalf("cliff start = %v, want 1", got)
	}
	if got := md.HeightAt(0.5, 1); math.Abs(got) > 1e-6 {
		t.Fatalf("south edge = %v, want 0", got)
	}
	mid := 1 - CliffBand/2
	if got := md.HeightAt(0.5, mid); math.Abs(got-0.5) > 1e-6 {
		t.Fatalf("cliff mid = %v, want 0.5", got)
	}
}

func TestSlopeFactorFromMapMatchesExplicitHeights(t *testing.T) {
	md := southDropMap()
	wx, wy := 0.5, 0.5
	got := SlopeFactor(md, wx, wy, 0, 1)
	want := SlopeFactorAt(wx, wy, 0, 1, func(x, y float64) float64 {
		if y < 1 {
			return 1
		}
		return 0
	})
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("map factor=%v explicit=%v", got, want)
	}
}

type slopeGolden struct {
	Name       string  `json:"name"`
	WX         float64 `json:"wx"`
	WY         float64 `json:"wy"`
	DX         int     `json:"dx"`
	DY         int     `json:"dy"`
	H0         float64 `json:"h0"`
	H1         float64 `json:"h1"`
	WantFactor float64 `json:"want_factor"`
}

func TestSlopeSpeedGolden(t *testing.T) {
	raw, err := os.ReadFile("../../../testdata/slope_speed_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []slopeGolden
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			got := SlopeFactorAt(tc.WX, tc.WY, tc.DX, tc.DY, func(x, y float64) float64 {
				if x == tc.WX && y == tc.WY {
					return tc.H0
				}
				return tc.H1
			})
			if math.Abs(got-tc.WantFactor) > 1e-9 {
				t.Fatalf("factor=%v want %v", got, tc.WantFactor)
			}
		})
	}
}

func southDropMap() *MapData {
	md := &MapData{Width: 2, Height: 2, CornerHeights: make([]byte, 9), CornerTypes: make([]byte, 9)}
	for i := 0; i < 3; i++ {
		md.CornerHeights[i] = 1
	}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = 3
	}
	return md
}
