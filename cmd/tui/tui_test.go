package main

import (
	"strings"
	"testing"

	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

func posComponent(x, y int32) *game.ComponentState {
	data, _ := pb.Marshal(&game.Position{X: x, Y: y})
	return &game.ComponentState{Component: "Position", Data: data}
}

func blockComponent(radius float64) *game.ComponentState {
	data, _ := pb.Marshal(&game.Block{Radius: radius})
	return &game.ComponentState{Component: "Block", Data: data}
}

// 全量快照重建实体表；地形按"格 (x,y) 读角点 (x,y)"与 config 对齐。
func TestWorldApplySnapshotAndConfig(t *testing.T) {
	w := newWorld()
	if w.ready() {
		t.Fatal("没有快照/配置时不应 ready")
	}
	// 17×17 角点，中间一列水
	corner := make([]byte, 17*17)
	for y := 0; y <= 16; y++ {
		corner[y*17+8] = byte(game.TerrainType_TERRAIN_TYPE_WATER)
	}
	w.applyConfig(&game.GameConfig{
		Map: &game.MapConfig{Width: 16, Height: 16, CornerTypes: corner},
		Templates: []*game.TemplateConfig{
			{Kind: game.ItemKind_ITEM_KIND_WOOD, Name: "木头"},
		},
	})
	if w.width != 16 || w.height != 16 {
		t.Fatalf("地图尺寸 = %d×%d, want 16×16", w.width, w.height)
	}
	if w.Walkable(8, 3) {
		t.Fatal("水格不应可走（与服务端 Walkable 同规则）")
	}
	if !w.Walkable(7, 3) || !w.Walkable(9, 3) {
		t.Fatal("水两侧应可走")
	}
	if w.Walkable(-1, 0) || w.Walkable(16, 0) {
		t.Fatal("越界不应可走")
	}
	if w.ready() {
		t.Fatal("只有配置、还没快照时不应 ready")
	}

	w.applySnapshot(&game.Snapshot{
		Tick:     42,
		DayCycle: &game.DayCycle{Phase: 7, Light: 0.5},
		Entities: []*game.EntityState{
			{EntityId: 1, Components: []*game.ComponentState{posComponent(3, 4), blockComponent(0.18)}},
			{EntityId: 2, Components: []*game.ComponentState{posComponent(3, 4)}},
		},
	})
	if len(w.entities) != 2 {
		t.Fatalf("实体数 = %d, want 2", len(w.entities))
	}
	if w.tick != 42 || w.dayLight != 0.5 {
		t.Fatalf("世界时钟 = %d light=%.2f", w.tick, w.dayLight)
	}
	// 同格两个实体：占位物优先级高于无组件实体
	if got := w.at(3, 4); got == nil || got.id != 1 {
		t.Fatalf("同格应优先渲染占位物, got %+v", got)
	}
	if !w.ready() {
		t.Fatal("快照到位后应 ready")
	}
	if name := w.kindName(game.ItemKind_ITEM_KIND_WOOD); name != "木头" {
		t.Fatalf("模板名 = %q, want 木头", name)
	}
}

// 增量：合并组件、移除组件、销毁实体。
func TestWorldApplyDelta(t *testing.T) {
	w := newWorld()
	w.applyConfig(&game.GameConfig{Map: &game.MapConfig{Width: 8, Height: 8, CornerTypes: make([]byte, 9*9)}})
	w.applySnapshot(&game.Snapshot{
		Tick: 1,
		Entities: []*game.EntityState{
			{EntityId: 1, Components: []*game.ComponentState{posComponent(1, 1), blockComponent(0.2)}},
		},
	})

	// 位置更新（只带 Position，不能把 Block 冲掉）
	w.applyDelta(&game.SnapshotDelta{
		Tick: 2,
		Entities: []*game.EntityState{
			{EntityId: 1, Components: []*game.ComponentState{posComponent(5, 6)}},
		},
	})
	e := w.entities[1]
	if e == nil || e.pos.X != 5 || e.pos.Y != 6 {
		t.Fatalf("位置应更新, got %+v", e)
	}
	if e.block == nil {
		t.Fatal("增量只带 Position 时不应清掉 Block")
	}

	// 移除组件
	w.applyDelta(&game.SnapshotDelta{
		Tick:              3,
		RemovedComponents: []*game.RemovedComponent{{EntityId: 1, Components: []string{"Block"}}},
	})
	if w.entities[1].block != nil {
		t.Fatal("Block 应被移除（树砍倒）")
	}

	// 销毁实体 + 未知实体上的移除不应 panic
	w.applyDelta(&game.SnapshotDelta{
		Tick:              4,
		RemovedEntities:   []uint64{1, 999},
		RemovedComponents: []*game.RemovedComponent{{EntityId: 999, Components: []string{"Block"}}},
	})
	if len(w.entities) != 0 {
		t.Fatalf("实体应被销毁, got %d", len(w.entities))
	}
}

// 渲染位置 = Position + 子格偏移；取整得到所在格。
func TestEntityTileFromSubOffset(t *testing.T) {
	e := &entity{
		pos:      &game.Position{X: 4, Y: 4},
		moveable: &game.Moveable{SubX: 0.75, SubY: 0.25},
	}
	if e.renderX() != 4.75 || e.renderY() != 4.25 {
		t.Fatalf("渲染位置 = (%.2f,%.2f)", e.renderX(), e.renderY())
	}
	if e.tileX() != 4 || e.tileY() != 4 {
		t.Fatalf("所在格 = (%d,%d), want (4,4)", e.tileX(), e.tileY())
	}
}

// e/p 键的目标选择：半径内取最近的，同距离取 id 小的（确定性）。
func TestNearbyTargetDeterministic(t *testing.T) {
	w := newWorld()
	w.applyConfig(&game.GameConfig{Map: &game.MapConfig{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)}})
	mkWorkable := func(id uint64, x, y int32) *game.EntityState {
		data, _ := pb.Marshal(&game.Workable{
			Kind: game.ItemKind_ITEM_KIND_WOOD, Action: game.WorkAction_WORK_ACTION_CHOP,
		})
		return &game.EntityState{EntityId: id, Components: []*game.ComponentState{
			posComponent(x, y), blockComponent(0.18),
			{Component: "Workable", Data: data},
		}}
	}
	w.applySnapshot(&game.Snapshot{Tick: 1, Entities: []*game.EntityState{
		mkWorkable(7, 3, 3),   // 距离 2
		mkWorkable(5, 3, 3),   // 距离 2（同距离，id 更小）
		mkWorkable(9, 10, 10), // 超出半径
	}})
	got := w.nearbyWorkable(1, 1, 6)
	if got == nil || got.id != 5 {
		t.Fatalf("应取同距离里 id 最小的 5, got %+v", got)
	}
	if w.nearbyWorkable(1, 1, 1) != nil {
		t.Fatal("半径外不应命中")
	}
	if w.nearbyLoot(1, 1, 6) != nil {
		t.Fatal("没有掉落物时不应命中")
	}
}

// 碰撞体叠加层不应 panic，且圆/盒/胶囊三种都能落在视口内。
func TestCollisionOverlaySmoke(t *testing.T) {
	w := newWorld()
	w.applyConfig(&game.GameConfig{Map: &game.MapConfig{Width: 32, Height: 32, CornerTypes: make([]byte, 33*33)}})
	boxData, _ := pb.Marshal(&game.Block{Width: 2, Height: 2})
	shapeData, _ := pb.Marshal(&game.DebugShape{
		Kind: game.DebugShape_DEBUG_SHAPE_KIND_CAPSULE, Radius: 0.25,
		AZ: -1, BZ: 1, AY: 0.6, BY: 0.6,
	})
	w.applySnapshot(&game.Snapshot{Tick: 1, Entities: []*game.EntityState{
		{EntityId: 1, Components: []*game.ComponentState{posComponent(5, 5), blockComponent(0.3)}},
		{EntityId: 2, Components: []*game.ComponentState{posComponent(10, 10), {Component: "Block", Data: boxData}}},
		{EntityId: 3, Components: []*game.ComponentState{
			posComponent(16, 16), {Component: "DebugShape", Data: shapeData},
		}},
	}})
	f := newFrame(40, 20)
	drawWorld(f, w, centerView(10, 10, 40, 18), true)
	drawHUD(f, w, true, "test")
	out := f.render(true)
	if len(out) == 0 {
		t.Fatal("渲染结果不应为空")
	}
}

// 地形字符映射（水/沙/岩/雪/草）。
func TestTerrainCellMapping(t *testing.T) {
	cases := map[game.TerrainType]rune{
		game.TerrainType_TERRAIN_TYPE_WATER: '~',
		game.TerrainType_TERRAIN_TYPE_SAND:  '.',
		game.TerrainType_TERRAIN_TYPE_ROCK:  '^',
		game.TerrainType_TERRAIN_TYPE_SNOW:  '*',
		game.TerrainType_TERRAIN_TYPE_GRASS: '"',
	}
	for terrain, want := range cases {
		if got, _ := terrainCell(terrain); got != want {
			t.Fatalf("%s → %q, want %q", terrain, got, want)
		}
	}
}

// 点到线段距离（胶囊叠加层用它判定覆盖格）。
func TestDistPointSegment(t *testing.T) {
	if d := distPointSegment(0, 1, -1, 0, 1, 0); d != 1 {
		t.Fatalf("垂直距离 = %.3f, want 1", d)
	}
	if d := distPointSegment(3, 0, -1, 0, 1, 0); d != 2 {
		t.Fatalf("端点外距离 = %.3f, want 2", d)
	}
	if d := distPointSegment(0, 0, 0, 0, 0, 0); d != 0 {
		t.Fatalf("退化段距离 = %.3f, want 0", d)
	}
}

// 枚举短名。
func TestShortEnum(t *testing.T) {
	cases := map[string]string{
		"ITEM_KIND_WOOD":           "WOOD",
		"CREATURE_KIND_WOLF":       "WOLF",
		"WORK_ACTION_CHOP":         "CHOP",
		"TERRAIN_TYPE_GRASS":       "GRASS",
		"DEBUG_SHAPE_KIND_CAPSULE": "CAPSULE",
		"SOMETHING_ELSE":           "SOMETHING_ELSE",
	}
	for in, want := range cases {
		if got := shortEnum(in); got != want {
			t.Fatalf("shortEnum(%q) = %q, want %q", in, got, want)
		}
	}
}

// 碰撞体叠加层必须真的标到格：树（格心圆）标中它自己那一格、
// 建筑（占格盒）标满整个足迹、四足胶囊按段+半径覆盖。
// 这条锁住"圆心在占格中心（anchor+0.5）而不是锚点"——半径 0.103 的树干
// 只有算对圆心才覆盖得到格心。
func TestOverlayMarksTreeBoxAndCapsule(t *testing.T) {
	w := newWorld()
	w.applyConfig(&game.GameConfig{Map: &game.MapConfig{Width: 32, Height: 32, CornerTypes: make([]byte, 33*33)}})

	woodData, _ := pb.Marshal(&game.WorkTarget{Kind: game.ItemKind_ITEM_KIND_WOOD, WorkLeft: 3, MaxWork: 3})
	boxData, _ := pb.Marshal(&game.Block{Width: 2, Height: 2})
	shapeData, _ := pb.Marshal(&game.DebugShape{
		Kind: game.DebugShape_DEBUG_SHAPE_KIND_CAPSULE, Radius: 0.25,
		AY: 0.6, BY: 0.6, AZ: -1, BZ: 1,
	})
	w.applySnapshot(&game.Snapshot{Tick: 1, Entities: []*game.EntityState{
		// 树：半径 0.103（真实配置值），锚点 (10,10)
		{EntityId: 1, Components: []*game.ComponentState{
			posComponent(10, 10), blockComponent(0.103),
			{Component: "Choppable", Data: woodData},
		}},
		// 建筑：2×2 盒，锚点 (20,20)
		{EntityId: 2, Components: []*game.ComponentState{
			posComponent(20, 20), {Component: "Block", Data: boxData},
		}},
		// 四足：胶囊沿局部 Z，节点在连续位置 (5.5, 5.5)
		{EntityId: 3, Components: []*game.ComponentState{
			posComponent(5, 5), {Component: "Moveable", Data: moveableData(t, 0.5, 0.5)},
			{Component: "DebugShape", Data: shapeData},
		}},
	}})

	f := newFrame(30, 20)
	v := centerView(15, 12, 30, 20) // 视口要同时覆盖 (5,5)/(10,10)/(20,20)
	drawWorld(f, w, v, true)

	marked := func(gx, gy int) bool {
		x, y := gx-v.x0, gy-v.y0
		if x < 0 || y < 0 || x >= f.w || y >= f.h {
			return false
		}
		return f.cells[y*f.w+x].bg != ""
	}
	// 树：只标中它自己那一格（半径远小于半格）
	if !marked(10, 10) {
		t.Fatal("树干应标中自己的格子（圆心在 anchor+0.5）")
	}
	if marked(11, 10) || marked(10, 11) {
		t.Fatal("半径 0.103 的树干不应溢出到相邻格")
	}
	// 建筑：2×2 全部标满
	for _, c := range [][2]int{{20, 20}, {21, 20}, {20, 21}, {21, 21}} {
		if !marked(c[0], c[1]) {
			t.Fatalf("2×2 建筑应标满足迹, 缺 (%d,%d)", c[0], c[1])
		}
	}
	if marked(22, 20) || marked(20, 22) {
		t.Fatal("建筑不应溢出足迹")
	}
	// 胶囊：节点 (5.5,5.5)，段沿 Z 从 4.5 到 6.5，半径 0.25 → 至少覆盖 (5,5)
	if !marked(5, 5) {
		t.Fatal("胶囊应覆盖节点所在格")
	}
	if marked(3, 5) {
		t.Fatal("胶囊不应覆盖到段外一格")
	}
}

// 关闭叠加层时不应有任何背景标记。
func TestOverlayOffMarksNothing(t *testing.T) {
	w := newWorld()
	w.applyConfig(&game.GameConfig{Map: &game.MapConfig{Width: 16, Height: 16, CornerTypes: make([]byte, 17*17)}})
	w.applySnapshot(&game.Snapshot{Tick: 1, Entities: []*game.EntityState{
		{EntityId: 1, Components: []*game.ComponentState{posComponent(5, 5), blockComponent(0.3)}},
	}})
	f := newFrame(16, 12)
	v := centerView(8, 8, 16, 10)
	drawWorld(f, w, v, false)
	for i, c := range f.cells {
		if c.bg != "" {
			t.Fatalf("格子 %d 不应有背景标记", i)
		}
	}
}

func moveableData(t *testing.T, subX, subY float64) []byte {
	t.Helper()
	data, err := pb.Marshal(&game.Moveable{Speed: 10, SubX: subX, SubY: subY, BodyRadius: 0.246})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// 连不上服务端时，错误提示必须给出可执行的一步——裸的 "connection refused"
// 完全没告诉人"TUI 只是客户端，服务端要另外起"。
func TestDialHintTellsYouToStartServer(t *testing.T) {
	hint := dialHint("ws://localhost:8081/ws")
	for _, want := range []string{"make run-gate", "ws://localhost:8081/ws", "-addr"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("提示里缺少 %q：\n%s", want, hint)
		}
	}
}
