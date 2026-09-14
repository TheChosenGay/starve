package worldmap

import (
	"fmt"
	"testing"

	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// splitMap 建一张 size×size 的全草地地图，中间一列水把图切成左右两半。
// 地形格 (x,y) 读角点 (x,y)，所以把角点第 size/2 列全部设成水即可封住那一列。
func splitMap(size int) *MapData {
	md := &MapData{Width: size, Height: size, CornerTypes: make([]byte, (size+1)*(size+1))}
	for y := 0; y <= size; y++ {
		md.CornerTypes[y*(size+1)+size/2] = byte(game.TerrainType_TERRAIN_TYPE_WATER)
	}
	md.InvalidateReachability()
	return md
}

// 连通性快查本身：同侧 true、跨水 false、不可走/越界 false。
// splitMap 的水在第 8 列，左半边是 x≤7、右半边是 x≥9。
func TestReachableComponents(t *testing.T) {
	md := splitMap(16)
	if !md.Reachable(1, 1, 1, 14) {
		t.Fatal("同侧（左）的两格应当连通")
	}
	if !md.Reachable(14, 1, 14, 14) {
		t.Fatal("同侧（右）的两格应当连通")
	}
	if md.Reachable(1, 8, 14, 8) {
		t.Fatal("被水切开的两侧不应连通")
	}
	if md.Reachable(8, 8, 1, 1) {
		t.Fatal("水格自身不是可走格，不应判为连通")
	}
	if md.Reachable(-1, 0, 1, 1) {
		t.Fatal("越界不应判为连通")
	}
	var nilMap *MapData
	if nilMap.Reachable(0, 0, 1, 1) {
		t.Fatal("nil 地图不应判为连通")
	}
}

// 跨分量寻路：直接判不可达，且**一次 A* 都不跑**（展开数 0）。
// 这是"每 tick 重新寻路的调用方不会把不可达变成每 tick 全图 A*"的关键保证。
func TestFindPathUnreachableSkipsSearch(t *testing.T) {
	for _, size := range []int{16, 64, 128} {
		md := splitMap(size)
		path, expanded := findPath(md, 1, 1, size-2, 1)
		if path != nil {
			t.Fatalf("size=%d 跨水不应有路径, got %v", size, path)
		}
		if expanded != 0 {
			t.Fatalf("size=%d 不可达应当靠快查直接判定，却展开了 %d 个节点", size, expanded)
		}
		if FindPath(md, 1, 1, size-2, 1) != nil { // 对外行为不变
			t.Fatalf("size=%d FindPath 跨水应返回 nil", size)
		}
	}
}

// 快查不能误杀：可达目标照旧给得出路径，终点正确。
func TestFindPathReachableStillWorks(t *testing.T) {
	md := splitMap(16)
	path, expanded := findPath(md, 1, 1, 6, 4)
	if len(path) == 0 {
		t.Fatal("同侧目标应当给得出路径")
	}
	if expanded == 0 {
		t.Fatal("可达目标应当真的跑 A*（展开数不应为 0）")
	}
	if x, y := walkPath(1, 1, path); x != 6 || y != 4 {
		t.Fatalf("路径终点 = (%d,%d), want (6,4)", x, y)
	}

	// 唯一通路（1 格宽缺口，在 y=8）仍然可达：快查判连通，A* 必须从缺口穿过。
	gap := &MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)}
	for y := 0; y <= 16; y++ {
		if y != 8 {
			gap.CornerTypes[y*17+8] = byte(game.TerrainType_TERRAIN_TYPE_WATER)
		}
	}
	gap.InvalidateReachability()
	through := FindPath(gap, 1, 1, 14, 14)
	if len(through) == 0 {
		t.Fatal("唯一通路仍应给得出路径")
	}
	if x, y := walkPath(1, 1, through); x != 14 || y != 14 {
		t.Fatalf("路径终点 = (%d,%d), want (14,14)", x, y)
	}
	crossed := false
	x, y := 1, 1
	for _, d := range through {
		x, y = x+d.DX, y+d.DY
		if x == 8 && y == 8 {
			crossed = true
		}
	}
	if !crossed {
		t.Fatalf("唯一通路必须从缺口 (8,8) 穿过: %v", through)
	}
}

// 占位物增删不改变连通性（占位格仍可走），所以索引在游戏过程中不需要失效——
// 这是它能长期缓存的前提。
func TestReachableIgnoresOccupied(t *testing.T) {
	md := splitMap(16)
	if !md.Reachable(1, 1, 7, 14) {
		t.Fatal("未占位时应连通")
	}
	md.ensureReach()          // 先建索引
	for x := 1; x <= 7; x++ { // 左半边通路上铺满占位物
		for y := 1; y < 15; y++ {
			md.SetOccupied(x, y, OccupiedCostFull)
		}
	}
	if !md.Reachable(1, 1, 7, 14) {
		t.Fatal("占位物不应影响连通性（占位 ≠ 不可走）")
	}
	if len(FindPath(md, 1, 1, 7, 14)) == 0 {
		t.Fatal("占位格代价再高也仍然给得出路径")
	}
}

// 地形变更 + InvalidateReachability 之后，快查必须看到新地形。
// 反过来，忘了失效会让快查按旧地形误判（可能错判"有路"从而退化成本，也可能错判
// "无路"从而错杀）——所以 attachMap 与任何改地形的入口都必须调用失效。
func TestInvalidateReachability(t *testing.T) {
	md := &MapData{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)}
	if !md.Reachable(1, 1, 14, 1) {
		t.Fatal("未改地形时应连通")
	}
	for y := 0; y <= 16; y++ {
		md.CornerTypes[y*17+8] = byte(game.TerrainType_TERRAIN_TYPE_WATER)
	}
	md.InvalidateReachability()
	if md.Reachable(1, 1, 14, 1) {
		t.Fatal("失效重建后应看到新地形：两侧已被水切开")
	}
	if FindPath(md, 1, 1, 14, 1) != nil {
		t.Fatal("被水切开后应判不可达")
	}
	// 再改回来（填平水道）→ 失效后重新连通
	for y := 0; y <= 16; y++ {
		md.CornerTypes[y*17+8] = byte(game.TerrainType_TERRAIN_TYPE_GRASS)
	}
	md.InvalidateReachability()
	if !md.Reachable(1, 1, 14, 1) {
		t.Fatal("填平水道后应重新连通")
	}
}

// 回归：快查不改变原有 A* 输出（占位代价该绕还是绕）。
func TestFindPathDetourShapeUnchanged(t *testing.T) {
	md := splitMap(16)
	md.SetOccupied(4, 4, OccupiedCostThin) // 一棵树干
	path := FindPath(md, 2, 4, 6, 4)
	if len(path) != 6 {
		t.Fatalf("绕行一格应为 6 步，实际 %d: %v", len(path), path)
	}
	x, y := 2, 4
	for _, d := range path {
		x, y = x+d.DX, y+d.DY
		if x == 4 && y == 4 {
			t.Fatalf("占位代价应让 A* 绕开 (4,4): %v", path)
		}
	}
	if x != 6 || y != 4 {
		t.Fatalf("路径终点 = (%d,%d), want (6,4)", x, y)
	}
}

// walkPath 按路径步进，返回终点格。
func walkPath(x, y int, path []components.MoveDir) (int, int) {
	for _, d := range path {
		x, y = x+d.DX, y+d.DY
	}
	return x, y
}

// 不可达查找的稳态成本：应当与地图规模无关（快查命中，不跑 A*）。
func BenchmarkFindPathUnreachable(b *testing.B) {
	for _, size := range []int{64, 128, 256} {
		b.Run(fmt.Sprintf("%dx%d", size, size), func(b *testing.B) {
			md := splitMap(size)
			md.ensureReach() // 预热索引：测的是稳态而不是首次建表
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if FindPath(md, 1, 1, size-2, 1) != nil {
					b.Fatal("不应有路径")
				}
			}
		})
	}
}

// 可达路径的对照基准（真的要跑 A*）。
func BenchmarkFindPathReachable(b *testing.B) {
	md := &MapData{Width: 128, Height: 128, CornerTypes: make([]byte, 129*129)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(FindPath(md, 1, 1, 126, 126)) == 0 {
			b.Fatal("应当有路径")
		}
	}
}
