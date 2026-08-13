package world

import (
	"os"
	"path/filepath"
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// newEffectTestWorld 构造带地图地块效果的最小世界（不启动 actor）。
func newEffectTestWorld(t *testing.T, tileEffects []byte, tileParams []int8, w, h int) *WorldActor {
	t.Helper()
	if len(tileEffects) != w*h {
		tileEffects = make([]byte, w*h)
	}
	if len(tileParams) != w*h {
		tileParams = make([]int8, w*h)
	}
	wa := NewWorldActor(WorldConfig{})
	wa.sim.AddResource(&MapData{Width: w, Height: h, TileEffects: tileEffects, TileParams: tileParams})
	return wa
}

// tickWorld 跑一轮全部系统（EffectSystem order 90 + MoveSystem order 95 都在内）。
func tickWorld(wa *WorldActor) {
	wa.sim.RunSystems(wa.cfg.TickInterval)
}

// addEmitter 摆一个效果发射器实体（效果集合 + 参数）。
func addEmitter(t *testing.T, wa *WorldActor, x, y, radius int, instances ...components.EffectInstance) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.EffectEmitter{Effects: instances, Radius: radius})
	return e
}

// 中毒地块（param=2）：进入后每 tick 掉 2 血，离开后停止。
func TestEffectSystemPoisonTile(t *testing.T) {
	wa := newEffectTestWorld(t, []byte{byte(components.EffectPoison)}, []int8{2}, 1, 1)
	e := wa.createPlayer("u1")
	ecs.Set(wa.sim, e, components.Position{X: 0, Y: 0})
	hp := ecs.Get[components.Health](wa.sim, e)
	eff := ecs.Get[components.Effects](wa.sim, e)

	tickWorld(wa)
	if hp.Cur != 98 {
		t.Fatalf("中毒第一 tick: hp=%d want 98", hp.Cur)
	}
	st := eff.Active[components.EffectPoison]
	if st.Count != 1 || st.Param != 2 {
		t.Fatalf("中毒状态=%+v want count=1 param=2", st)
	}
	tickWorld(wa)
	if hp.Cur != 96 {
		t.Fatalf("中毒第二 tick: hp=%d want 96", hp.Cur)
	}

	// 离开毒格（越界 → 无地块效果）
	ecs.Set(wa.sim, e, components.Position{X: 1, Y: 0})
	tickWorld(wa)
	if hp.Cur != 96 {
		t.Fatalf("离开毒格后不应再扣血: hp=%d", hp.Cur)
	}
	if eff.Active[components.EffectPoison].Count != 0 {
		t.Fatalf("离开后中毒计数=%d want 0", eff.Active[components.EffectPoison].Count)
	}
}

// 发射器覆盖：多源叠加计数 + 参数求和（1+2=3 血/tick）→ 逐个移除递减 → 全部移除才 OnExit。
func TestEffectSystemEmitterCoverage(t *testing.T) {
	wa := newEffectTestWorld(t, nil, nil, 8, 8)
	e := wa.createPlayer("u1")
	ecs.Set(wa.sim, e, components.Position{X: 2, Y: 2})
	hp := ecs.Get[components.Health](wa.sim, e)
	eff := ecs.Get[components.Effects](wa.sim, e)

	em1 := addEmitter(t, wa, 1, 2, 1,
		components.EffectInstance{Order: components.EffectSpeed, Param: 100},
		components.EffectInstance{Order: components.EffectPoison, Param: 1},
	)
	em2 := addEmitter(t, wa, 2, 1, 1,
		components.EffectInstance{Order: components.EffectPoison, Param: 2},
	)

	tickWorld(wa)
	if st := eff.Active[components.EffectPoison]; st.Count != 2 || st.Param != 3 {
		t.Fatalf("多源中毒状态=%+v want count=2 param=3", st)
	}
	if st := eff.Active[components.EffectSpeed]; st.Count != 1 || st.Param != 100 {
		t.Fatalf("速度状态=%+v want count=1 param=100", st)
	}
	if hp.Cur != 97 {
		t.Fatalf("中毒 tick（3 血）: hp=%d want 97", hp.Cur)
	}

	// 移除一个源 → 计数 1、参数 1，效果仍生效
	wa.sim.DestroyEntity(em2)
	tickWorld(wa)
	if st := eff.Active[components.EffectPoison]; st.Count != 1 || st.Param != 1 {
		t.Fatalf("移除一个源后中毒状态=%+v want count=1 param=1", st)
	}
	if hp.Cur != 96 {
		t.Fatalf("仍覆盖应继续扣血: hp=%d want 96", hp.Cur)
	}

	// 全部移除 → OnExit，掉血停止
	wa.sim.DestroyEntity(em1)
	tickWorld(wa)
	if eff.Active[components.EffectPoison].Count != 0 || eff.Active[components.EffectSpeed].Count != 0 {
		t.Fatalf("全部移除后 active=%v want 空", eff.Active)
	}
	if hp.Cur != 96 {
		t.Fatalf("离开覆盖后不应再扣血: hp=%d", hp.Cur)
	}
}

// 半径 0 发射器只影响自身所在格：同格生效，走开即解除。
func TestEffectSystemEmitterRadiusZero(t *testing.T) {
	wa := newEffectTestWorld(t, nil, nil, 5, 5)
	e := wa.createPlayer("u1")
	ecs.Set(wa.sim, e, components.Position{X: 3, Y: 3})
	eff := ecs.Get[components.Effects](wa.sim, e)
	addEmitter(t, wa, 3, 3, 0, components.EffectInstance{Order: components.EffectSpeed, Param: -50})

	tickWorld(wa)
	if eff.Active[components.EffectSpeed].Count != 1 {
		t.Fatalf("同格速度效果计数=%d want 1", eff.Active[components.EffectSpeed].Count)
	}
	ecs.Set(wa.sim, e, components.Position{X: 4, Y: 3})
	tickWorld(wa)
	if eff.Active[components.EffectSpeed].Count != 0 {
		t.Fatalf("走开后速度效果计数=%d want 0", eff.Active[components.EffectSpeed].Count)
	}
}

// 加速（speed param=+100）：步进间隔 2 → 1，move 指令后 1 tick 走 1 格。
func TestEffectSpeedUpMove(t *testing.T) {
	wa := newEffectTestWorld(t, nil, nil, 10, 10)
	e := wa.createPlayer("u1")
	ecs.Set(wa.sim, e, components.Position{X: 5, Y: 5})
	addEmitter(t, wa, 5, 5, 1, components.EffectInstance{Order: components.EffectSpeed, Param: 100})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: e, DX: 1, DY: 0}})
	tickWorld(wa)
	p := ecs.Get[components.Position](wa.sim, e)
	if p.X != 6 || p.Y != 5 {
		t.Fatalf("加速移动 pos=(%d,%d) want (6,5)", p.X, p.Y)
	}
}

// 减速（speed param=-50）：步进间隔 2 → 4，2 tick 不动、4 tick 走 1 格。
func TestEffectSlowDownMove(t *testing.T) {
	wa := newEffectTestWorld(t, nil, nil, 10, 10)
	e := wa.createPlayer("u1")
	ecs.Set(wa.sim, e, components.Position{X: 5, Y: 5})
	eff := ecs.Get[components.Effects](wa.sim, e)
	addEmitter(t, wa, 5, 5, 1, components.EffectInstance{Order: components.EffectSpeed, Param: -50})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: e, DX: 1, DY: 0}})
	tickWorld(wa)
	tickWorld(wa)
	p := ecs.Get[components.Position](wa.sim, e)
	if p.X != 5 {
		t.Fatalf("减速 2 tick 后不应移动: pos=(%d,%d)", p.X, p.Y)
	}
	if st := eff.Active[components.EffectSpeed]; st.Count != 1 || st.Param != -50 {
		t.Fatalf("减速状态=%+v want count=1 param=-50", st)
	}
	tickWorld(wa)
	tickWorld(wa)
	p = ecs.Get[components.Position](wa.sim, e)
	if p.X != 6 {
		t.Fatalf("减速 4 tick 后应走 1 格: pos=(%d,%d)", p.X, p.Y)
	}
}

// 移动队列：命令进缓存按序消费；停止命令清空队列。
func TestMoveQueueAndStop(t *testing.T) {
	wa := newEffectTestWorld(t, nil, nil, 10, 10)
	e := wa.createPlayer("u1")
	ecs.Set(wa.sim, e, components.Position{X: 0, Y: 0})

	// 连发两条同向 + 一条反向：应顺序执行（右、右、上）
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: e, DX: 1, DY: 0}})
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: e, DX: 1, DY: 0}})
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: e, DX: 0, DY: 1}})
	mv := ecs.Get[components.Moveable](wa.sim, e)
	if len(mv.Queue) != 3 {
		t.Fatalf("队列长度=%d want 3", len(mv.Queue))
	}
	// 3 个命令 × 间隔 2 = 6 tick 全部走完
	for i := 0; i < 6; i++ {
		tickWorld(wa)
	}
	p := ecs.Get[components.Position](wa.sim, e)
	if p.X != 2 || p.Y != 1 {
		t.Fatalf("队列顺序执行后 pos=(%d,%d) want (2,1)", p.X, p.Y)
	}

	// 停止：清空队列
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: e, DX: 1, DY: 0}})
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: e, DX: 0, DY: 0}})
	if mv = ecs.Get[components.Moveable](wa.sim, e); len(mv.Queue) != 0 {
		t.Fatalf("停止后队列应清空: %d", len(mv.Queue))
	}
}

// 地图生成：手摆 effect_tiles 写入效果与参数，并保留长度契约 W×H。
func TestMapGenTileEffects(t *testing.T) {
	spec := &MapSpec{
		Width:  5,
		Height: 5,
		SpawnX: 2,
		SpawnY: 2,
		Terrain: HeightSpec{
			Hills:           2,
			MaxAmp:          2,
			RockLevel:       6,
			WaterLevel:      1,
			SpawnFlatRadius: 1,
		},
		Handplaced: HandplacedSpec{
			EffectTiles: []EffectTileSeed{{Effect: "poison", Param: 3, X: 4, Y: 0}},
		},
	}
	res := worldmap.NewMapGenerator(42, spec, nil).Generate()
	if len(res.TileEffects) != 5*5 || len(res.TileParams) != 5*5 {
		t.Fatalf("TileEffects/TileParams 长度=%d/%d want 25/25", len(res.TileEffects), len(res.TileParams))
	}
	if res.TileEffects[4] != byte(components.EffectPoison) || res.TileParams[4] != 3 {
		t.Fatalf("手摆毒格 (4,0) effect=%d param=%d want poison/3", res.TileEffects[4], res.TileParams[4])
	}
}

// 存档往返：TileEffects/TileParams 随档保存/恢复（服务端内部数据，不进端上契约）。
func TestSaveLoadTileEffects(t *testing.T) {
	p := filepath.Join(t.TempDir(), "map.json")
	if err := os.WriteFile(p, []byte(effectTestMapJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	wa := NewWorldActor(WorldConfig{MapPath: p, MapSeed: 42})
	if o, prm := wa.tileEffectAt(4, 0); o != components.EffectPoison || prm != 3 {
		t.Fatalf("生成地图 (4,0) effect=%d param=%d want poison/3", o, prm)
	}
	data := wa.Save()

	wa2 := NewWorldActor(WorldConfig{})
	if err := wa2.Load(data); err != nil {
		t.Fatal(err)
	}
	if o, prm := wa2.tileEffectAt(4, 0); o != components.EffectPoison || prm != 3 {
		t.Fatalf("加载后 (4,0) effect=%d param=%d want poison/3", o, prm)
	}
	if o, prm := wa2.tileEffectAt(5, 0); o != 0 || prm != 0 {
		t.Fatal("越界查询应返回无效果")
	}
}

const effectTestMapJSON = `{
  "width": 5,
  "height": 5,
  "spawn_x": 2,
  "spawn_y": 2,
  "terrain": { "hills": 2, "max_amp": 2, "rock_level": 6, "water_level": 1, "spawn_flat_radius": 1 },
  "handplaced": {
    "effect_tiles": [ { "effect": "poison", "param": 3, "x": 4, "y": 0 } ]
  },
  "scatter": []
}`
