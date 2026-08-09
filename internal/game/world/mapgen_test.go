package world

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/actor"
	game "starve/pkg/proto/game"
)

const testMapJSON = `{
  "width": 20,
  "height": 20,
  "spawn_x": 10,
  "spawn_y": 10,
  "terrain": { "hills": 4, "max_amp": 4, "rock_level": 5, "water_level": 1, "spawn_flat_radius": 5 },
  "handplaced": {
    "resources": [ { "kind": "berry", "x": 9, "y": 9, "action": "pick", "work": 3 } ],
    "stations": [ { "type": "campfire", "x": 11, "y": 11 } ],
    "loot": [ { "kind": "wood", "count": 3, "x": 9, "y": 11 } ]
  },
  "scatter": [
    { "kind": "berry", "action": "pick", "work": 3, "count": 6, "min_dist": 2 },
    { "kind": "wood", "action": "chop", "work": 5, "count": 4, "min_dist": 3 }
  ]
}`

func testMapPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "map.json")
	if err := os.WriteFile(p, []byte(testMapJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func genMap(t *testing.T, seed uint64) *MapResult {
	t.Helper()
	spec, err := loadMapSpec(testMapPath(t))
	if err != nil {
		t.Fatal(err)
	}
	return (&MapGenerator{seed: seed, spec: spec}).Generate()
}

func TestMapGenerateDeterministic(t *testing.T) {
	a := genMap(t, 42)
	b := genMap(t, 42)
	if !bytes.Equal(a.CornerHeights, b.CornerHeights) || !bytes.Equal(a.CornerTypes, b.CornerTypes) {
		t.Fatal("同 seed 应生成相同地形")
	}
	if len(a.Resources) != len(b.Resources) {
		t.Fatalf("同 seed 资源数应一致: %d vs %d", len(a.Resources), len(b.Resources))
	}
	c := genMap(t, 43)
	if bytes.Equal(a.CornerHeights, c.CornerHeights) {
		t.Fatal("不同 seed 应生成不同地形")
	}
}

func TestMapSpawnFlat(t *testing.T) {
	res := genMap(t, 42)
	cw := res.Width + 1
	r := 5
	for y := 0; y <= res.Height; y++ {
		for x := 0; x <= res.Width; x++ {
			dx := x - res.SpawnX
			dy := y - res.SpawnY
			if dx*dx+dy*dy <= r*r && res.CornerHeights[y*cw+x] != 0 {
				t.Fatalf("出生点压平区 (%d,%d) 高度 = %d, want 0", x, y, res.CornerHeights[y*cw+x])
			}
		}
	}
}

func TestMapAdjacentDiffLe1(t *testing.T) {
	res := genMap(t, 42)
	cw := res.Width + 1
	for y := 0; y <= res.Height; y++ {
		for x := 0; x <= res.Width; x++ {
			i := y*cw + x
			if x+1 <= res.Width && abs(int(res.CornerHeights[i])-int(res.CornerHeights[i+1])) > 1 {
				t.Fatalf("相邻角高度差 > 1 at (%d,%d)", x, y)
			}
			if y+1 <= res.Height && abs(int(res.CornerHeights[i])-int(res.CornerHeights[i+cw])) > 1 {
				t.Fatalf("相邻角高度差 > 1 at (%d,%d)", x, y)
			}
		}
	}
}

func TestMapEntitiesInBounds(t *testing.T) {
	res := genMap(t, 42)
	for _, r := range res.Resources {
		if r.x < 0 || r.x >= res.Width || r.y < 0 || r.y >= res.Height {
			t.Fatalf("资源越界: (%d,%d) in %dx%d", r.x, r.y, res.Width, res.Height)
		}
	}
	for _, s := range res.Stations {
		if s.X < 0 || s.X >= res.Width || s.Y < 0 || s.Y >= res.Height {
			t.Fatalf("工作站越界: (%d,%d)", s.X, s.Y)
		}
	}
	for _, l := range res.Loot {
		if l.X < 0 || l.X >= res.Width || l.Y < 0 || l.Y >= res.Height {
			t.Fatalf("物资越界: (%d,%d)", l.X, l.Y)
		}
	}
}

func TestMapSaveLoad(t *testing.T) {
	wa := NewWorldActor(WorldConfig{MapPath: testMapPath(t), MapSeed: 42})
	data := wa.Save()
	var sd game.SaveData
	if err := pb.Unmarshal(data, &sd); err != nil {
		t.Fatal(err)
	}
	if sd.Map == nil || len(sd.Map.CornerHeights) == 0 {
		t.Fatal("存档应包含地形")
	}
	if sd.Meta.MapSeed != 42 {
		t.Fatalf("存档 map_seed = %d, want 42", sd.Meta.MapSeed)
	}

	wa2 := NewWorldActor(WorldConfig{}) // 不生成地形
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}
	if wa2.mapConfig == nil || !bytes.Equal(wa2.mapConfig.CornerHeights, sd.Map.CornerHeights) {
		t.Fatal("加载后地形应与存档一致")
	}
}

func TestMapQueryConfigIncludesTerrain(t *testing.T) {
	eng := actor.NewEngine(actor.Config{})
	defer eng.Shutdown()
	wa := NewWorldActor(WorldConfig{MapPath: testMapPath(t), MapSeed: 42})
	pid := eng.Spawn(func() actor.IActor { return wa }, "world", "m")
	resp := eng.Request(pid, QueryConfig{}, time.Second)
	v, err := resp.Wait()
	if err != nil {
		t.Fatal(err)
	}
	pc, ok := v.(*game.GameConfig)
	if !ok {
		t.Fatalf("类型错误: %T", v)
	}
	if pc.Map == nil || pc.Map.Width != 20 || pc.Map.Height != 20 {
		t.Fatalf("world.config 应含地形: %+v", pc.Map)
	}
	if len(pc.Map.CornerHeights) != 21*21 {
		t.Fatalf("角高度数 = %d, want %d", len(pc.Map.CornerHeights), 21*21)
	}
}
