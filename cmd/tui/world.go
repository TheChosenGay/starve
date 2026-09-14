package main

import (
	"sort"

	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// entity 是一个实体的本地投影：只保留渲染与交互需要的组件，其余忽略。
// 协议本身是"客户端按名解析"，加组件不会影响这里。
type entity struct {
	id       uint64
	pos      *game.Position
	moveable *game.Moveable
	block    *game.Block
	shape    *game.DebugShape
	player   *game.Player
	creature *game.Creature
	health   *game.Health
	// 可作业目标：环境物挂 Choppable/Minable/Pickable（受激能力），
	// 旧档可能是 Workable——两者都归一到这里，渲染与 e 键只认这几个字段。
	hasWork    bool
	workKind   game.ItemKind
	workAction game.WorkAction
	workLeft   int32
	workMax    int32

	building *game.Building
	station  *game.Workstation
	loot     *game.Loot
	growable *game.Growable
	dead     *game.Dead
	ai       *game.AI
}

// 渲染位置：Position + 子格偏移（服务端连续位置就以锚点为原点，见 move_step.go）。
func (e *entity) renderX() float64 {
	if e.moveable != nil {
		return float64(e.pos.X) + e.moveable.SubX
	}
	return float64(e.pos.X)
}

func (e *entity) renderY() float64 {
	if e.moveable != nil {
		return float64(e.pos.Y) + e.moveable.SubY
	}
	return float64(e.pos.Y)
}

// tileX/tileY 渲染位置落在哪一格（子格 < 1，所以直接取整就是所在格）。
func (e *entity) tileX() int { return int(e.renderX()) }
func (e *entity) tileY() int { return int(e.renderY()) }

func (e *entity) isPlayer() bool { return e.player != nil }

// world 是 TUI 侧的只读世界镜像：地形 + 实体表 + 世界时钟 + 端上配置。
// 只被主循环（以及 -dump 的等待循环）单线程访问，所以不加锁。
type world struct {
	width, height int
	cornerTypes   []byte // (W+1)×(H+1) 每角 TerrainType
	cornerHeights []byte // (W+1)×(H+1) 每角高度（0..255，服务端坡度因子用它）
	entities      map[uint64]*entity
	dayPhase      int32   // 世界时钟相位（DayCycle）
	dayLight      float32 // 0..1 光照
	tick          uint64
	own           uint64

	templates map[int32]*game.TemplateConfig
	buildings map[int32]*game.BuildingConfig
	stations  map[int32]*game.StationConfig
	haveCfg   bool
	haveSnap  bool
}

func newWorld() *world {
	return &world{
		entities:  make(map[uint64]*entity),
		templates: make(map[int32]*game.TemplateConfig),
		buildings: make(map[int32]*game.BuildingConfig),
		stations:  make(map[int32]*game.StationConfig),
	}
}

// ready 世界配置与快照都到了才能画（地形来自 config，实体来自 snapshot）。
func (w *world) ready() bool { return w.haveCfg && w.haveSnap }

func (w *world) setOwn(id uint64) { w.own = id }

func (w *world) ownEntity() *entity { return w.entities[w.own] }

// TileType 该格地形（与服务端 terrainWalkable 同规则：格 (x,y) 读角点 (x,y)）。
func (w *world) TileType(x, y int) game.TerrainType {
	if x < 0 || y < 0 || x >= w.width || y >= w.height {
		return game.TerrainType_TERRAIN_TYPE_UNSPECIFIED
	}
	if len(w.cornerTypes) != (w.width+1)*(w.height+1) {
		return game.TerrainType_TERRAIN_TYPE_GRASS
	}
	return game.TerrainType(w.cornerTypes[y*(w.width+1)+x])
}

// HeightAt 该角点高度（与服务端同一套角点采样；无数据 = 0）。
// 移动速度会被坡度因子修正，所以"为什么这里走得慢"要看它。
func (w *world) HeightAt(x, y int) int {
	if x < 0 || y < 0 || x > w.width || y > w.height {
		return 0
	}
	if len(w.cornerHeights) != (w.width+1)*(w.height+1) {
		return 0
	}
	return int(w.cornerHeights[y*(w.width+1)+x])
}

// Walkable 与服务端一致：只看地形（水/悬崖不可走），占位物不算墙。
func (w *world) Walkable(x, y int) bool {
	if x < 0 || y < 0 || x >= w.width || y >= w.height {
		return false
	}
	return w.TileType(x, y) != game.TerrainType_TERRAIN_TYPE_WATER
}

// at 返回该格上用于渲染的实体（优先级：自己 > 玩家 > 生物 > 掉落 > 占位物 > 建筑）。
func (w *world) at(x, y int) *entity {
	var best *entity
	bestRank := -1
	for _, e := range w.entities {
		if e.pos == nil || e.tileX() != x || e.tileY() != y {
			continue
		}
		if r := w.rank(e); r > bestRank {
			best, bestRank = e, r
		}
	}
	return best
}

// maxEntityRank 是 rank() 的上限，绘制按 1..maxEntityRank 分层（见 render.go）。
const maxEntityRank = 6

func (w *world) rank(e *entity) int {
	switch {
	case e.id == w.own:
		return maxEntityRank
	case e.isPlayer():
		return 5
	case e.creature != nil:
		return 4
	case e.loot != nil:
		return 3
	case e.block != nil:
		return 2
	case e.building != nil || e.station != nil:
		return 1
	default:
		return 0
	}
}

// nearbyWorkable 返回半径内最近的可作业目标（供 e 键使用）。
func (w *world) nearbyWorkable(x, y, radius int) *entity {
	return w.nearest(x, y, radius, func(e *entity) bool { return e.hasWork })
}

// nearbyLoot 返回半径内最近的掉落物（供 p 键使用）。
func (w *world) nearbyLoot(x, y, radius int) *entity {
	return w.nearest(x, y, radius, func(e *entity) bool { return e.loot != nil })
}

func (w *world) nearest(x, y, radius int, ok func(*entity) bool) *entity {
	var best *entity
	bestD := radius + 1
	for _, e := range w.entities {
		if e.pos == nil || e.dead != nil || !ok(e) {
			continue
		}
		d := absInt(e.tileX()-x) + absInt(e.tileY()-y)
		// 确定性：同距离取实体 id 小的
		if d < bestD || (d == bestD && best != nil && e.id < best.id) {
			best, bestD = e, d
		}
	}
	return best
}

// counts 统计视野内的实体种类（状态行显示用）。
func (w *world) counts() (workable, creature, loot, other int) {
	for _, e := range w.entities {
		switch {
		case e.creature != nil:
			creature++
		case e.loot != nil:
			loot++
		case e.hasWork:
			workable++
		case e.isPlayer() && e.id != w.own:
			other++
		}
	}
	return
}

// ---- 快照应用 ----

func (w *world) applySnapshot(s *game.Snapshot) {
	w.entities = make(map[uint64]*entity, len(s.Entities))
	for _, es := range s.Entities {
		w.applyEntity(es, false)
	}
	if s.DayCycle != nil {
		w.dayPhase, w.dayLight = s.DayCycle.Phase, s.DayCycle.Light
	}
	w.tick = s.Tick
	w.haveSnap = true
}

func (w *world) applyDelta(d *game.SnapshotDelta) {
	for _, id := range d.RemovedEntities {
		delete(w.entities, id)
	}
	for _, es := range d.Entities {
		w.applyEntity(es, true)
	}
	for _, rc := range d.RemovedComponents {
		e := w.entities[rc.EntityId]
		if e == nil {
			continue
		}
		for _, name := range rc.Components {
			e.clearComponent(name)
		}
	}
	if d.DayCycle != nil {
		w.dayPhase, w.dayLight = d.DayCycle.Phase, d.DayCycle.Light
	}
	w.tick = d.Tick
	w.haveSnap = true
}

func (w *world) applyConfig(cfg *game.GameConfig) {
	if m := cfg.Map; m != nil {
		w.width, w.height = int(m.Width), int(m.Height)
		w.cornerTypes = m.CornerTypes
		w.cornerHeights = m.CornerHeights
	}
	for _, t := range cfg.Templates {
		w.templates[int32(t.Kind)] = t
	}
	for _, b := range cfg.Buildings {
		w.buildings[int32(b.Kind)] = b
	}
	for _, s := range cfg.Stations {
		w.stations[int32(s.Type)] = s
	}
	w.haveCfg = true
}

// applyEntity 写入/合并一个实体（merge=true 时只覆盖本次带来的组件）。
func (w *world) applyEntity(es *game.EntityState, merge bool) {
	e := w.entities[es.EntityId]
	if e == nil || !merge {
		e = &entity{id: es.EntityId}
		w.entities[es.EntityId] = e
	}
	for _, cs := range es.Components {
		e.applyComponent(cs)
	}
}

func (e *entity) applyComponent(cs *game.ComponentState) {
	switch cs.Component {
	case "Position":
		var v game.Position
		if pbUnmarshal(cs.Data, &v) {
			e.pos = &v
		}
	case "Moveable":
		var v game.Moveable
		if pbUnmarshal(cs.Data, &v) {
			e.moveable = &v
		}
	case "Block":
		var v game.Block
		if pbUnmarshal(cs.Data, &v) {
			e.block = &v
		}
	case "DebugShape":
		var v game.DebugShape
		if pbUnmarshal(cs.Data, &v) {
			e.shape = &v
		}
	case "Player":
		var v game.Player
		if pbUnmarshal(cs.Data, &v) {
			e.player = &v
		}
	case "Creature":
		var v game.Creature
		if pbUnmarshal(cs.Data, &v) {
			e.creature = &v
		}
	case "Health":
		var v game.Health
		if pbUnmarshal(cs.Data, &v) {
			e.health = &v
		}
	case "Choppable", "Minable", "Pickable":
		var v game.WorkTarget
		if pbUnmarshal(cs.Data, &v) {
			e.hasWork = true
			e.workKind = v.Kind
			e.workLeft, e.workMax = v.WorkLeft, v.MaxWork
			switch cs.Component {
			case "Choppable":
				e.workAction = game.WorkAction_WORK_ACTION_CHOP
			case "Minable":
				e.workAction = game.WorkAction_WORK_ACTION_MINE
			default:
				e.workAction = game.WorkAction_WORK_ACTION_PICK
			}
		}
	case "Workable": // 旧档遗留形态
		var v game.Workable
		if pbUnmarshal(cs.Data, &v) {
			e.hasWork = true
			e.workKind, e.workAction = v.Kind, v.Action
			e.workLeft, e.workMax = v.WorkLeft, v.MaxWork
		}
	case "Building":
		var v game.Building
		if pbUnmarshal(cs.Data, &v) {
			e.building = &v
		}
	case "Workstation":
		var v game.Workstation
		if pbUnmarshal(cs.Data, &v) {
			e.station = &v
		}
	case "Loot":
		var v game.Loot
		if pbUnmarshal(cs.Data, &v) {
			e.loot = &v
		}
	case "Growable":
		var v game.Growable
		if pbUnmarshal(cs.Data, &v) {
			e.growable = &v
		}
	case "Dead":
		var v game.Dead
		if pbUnmarshal(cs.Data, &v) {
			e.dead = &v
		}
	case "AI":
		var v game.AI
		if pbUnmarshal(cs.Data, &v) {
			e.ai = &v
		}
	}
}

func (e *entity) clearComponent(name string) {
	switch name {
	case "Position":
		e.pos = nil
	case "Moveable":
		e.moveable = nil
	case "Block":
		e.block = nil
	case "DebugShape":
		e.shape = nil
	case "Player":
		e.player = nil
	case "Creature":
		e.creature = nil
	case "Health":
		e.health = nil
	case "Choppable", "Minable", "Pickable", "Workable":
		e.hasWork = false
		e.workKind, e.workAction, e.workLeft, e.workMax = 0, 0, 0, 0
	case "Building":
		e.building = nil
	case "Workstation":
		e.station = nil
	case "Loot":
		e.loot = nil
	case "Growable":
		e.growable = nil
	case "Dead":
		e.dead = nil
	case "AI":
		e.ai = nil
	}
}

// sortedEntities 按 id 升序返回实体（渲染与列表的确定性顺序）。
func (w *world) sortedEntities() []*entity {
	out := make([]*entity, 0, len(w.entities))
	for _, e := range w.entities {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// kindName 取配置里的中文名（world.config 的模板表），没有就退回枚举名。
func (w *world) kindName(kind game.ItemKind) string {
	if t, ok := w.templates[int32(kind)]; ok && t.Name != "" {
		return t.Name
	}
	return shortEnum(kind.String())
}

// pbUnmarshal 解组件值；失败返回 false（未知/损坏的组件忽略，不影响其它实体）。
func pbUnmarshal(data []byte, v pb.Message) bool { return pb.Unmarshal(data, v) == nil }

// shortEnum 把 "ITEM_KIND_WOOD" / "CREATURE_KIND_WOLF" 之类压成 "WOOD" / "WOLF"。
func shortEnum(name string) string {
	for _, prefix := range []string{
		"ITEM_KIND_", "CREATURE_KIND_", "BUILDING_KIND_", "WORK_ACTION_",
		"TERRAIN_TYPE_", "WORKSTATION_TYPE_", "DEBUG_SHAPE_KIND_",
	} {
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			return name[len(prefix):]
		}
	}
	return name
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
