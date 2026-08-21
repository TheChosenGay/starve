package worldmap

import (
	"reflect"
	"testing"

	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

func TestNearbyWalkableDeterministicAndFallback(t *testing.T) {
	md := &MapData{Width: 3, Height: 3, CornerTypes: make([]byte, 16)}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = byte(game.TerrainType_TERRAIN_TYPE_WATER)
	}
	// origin 和右侧是仅有可走格；采样排除 origin，因此第二、三个 stack 回退并重叠在 origin。
	md.CornerTypes[1*4+1] = byte(game.TerrainType_TERRAIN_TYPE_GRASS)
	md.CornerTypes[1*4+2] = byte(game.TerrainType_TERRAIN_TYPE_GRASS)
	origin := components.Position{X: 1, Y: 1}
	got := md.NearbyWalkable(origin, 3, 2, 123)
	if !reflect.DeepEqual(got, md.NearbyWalkable(origin, 3, 2, 123)) {
		t.Fatal("相同 seed 的附近位置采样必须确定")
	}
	if got[0] != (components.Position{X: 2, Y: 1}) || got[1] != origin || got[2] != origin {
		t.Fatalf("可走位置与回退策略错误: %+v", got)
	}
}
