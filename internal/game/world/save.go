package world

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/systems"
	"starve/internal/game/worldmap"
	game "starve/pkg/proto/game"
)

// SaveVersion 存档格式版本（未来兼容演进）。
const SaveVersion = "starve-save-v2"

// SaveRequest 请求保存：返回存档字节（请求-应答）。
// 保存统一走 actor 消息（线性模型）：客户端点存档、关服保存都经此入口。
type SaveRequest struct {
	Trigger SaveTrigger
}

// Save 导出世界为存档字节（实体+组件快照 + 世界元数据）。
// 只能在世界 actor goroutine 上调用（SaveRequest 或 onTick 内），保证线性。
func (a *WorldActor) Save() []byte {
	return a.SaveWithTrigger(SaveTriggerManual)
}

// SaveWithTrigger 导出存档并报告有界来源；空来源按 manual 处理。
func (a *WorldActor) SaveWithTrigger(trigger SaveTrigger) []byte {
	if trigger == "" {
		trigger = SaveTriggerManual
	}
	startedAt := time.Now()
	b, err := a.marshalSave()
	a.observeSave(SaveStats{
		Duration: time.Since(startedAt),
		Bytes:    len(b),
		Trigger:  trigger,
		Err:      err,
	})
	return b
}

func (a *WorldActor) marshalSave() ([]byte, error) {
	ids := a.sim.ExportIDs()
	journal, _ := json.Marshal(a.journal)
	data := &game.SaveData{
		Snapshot: FullSnapshot(a.sim),
		Meta: &game.WorldMeta{
			Tick:         uint64(a.tick),
			NextEntityId: ids.Next,
			FreeIds:      entitiesToUint64(ids.Free),
			Version:      SaveVersion,
			MapSeed:      a.config.MapSeed,
		},
		Journal: journal,
		Map:     a.mapConfig,
	}
	if md, ok := ecs.TryResource[MapData](a.sim); ok {
		data.TileEffects = md.TileEffects
		data.TileParams = int8sToBytes(md.TileParams)
		data.TileRegions = md.RegionIDs
		data.RegionWeather = weatherBiasToProto(md.RegionWeather)
		data.RegionBiomes = append([]game.BiomeType(nil), md.RegionBiomes...)
	}
	b, err := pb.Marshal(data)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Load 从存档字节恢复世界（必须在 Start 之前调用）。
// 恢复：ID 分配状态 → 实体/组件 → 世界时钟 → 昼夜 → 玩家所有权表。
func (a *WorldActor) Load(data []byte) error {
	var sd game.SaveData
	if err := pb.Unmarshal(data, &sd); err != nil {
		return fmt.Errorf("world: 存档解析失败: %w", err)
	}
	if sd.Snapshot == nil || sd.Meta == nil {
		return errors.New("world: 存档缺少快照或元数据")
	}

	// 构造器可能已按同配置生成初始实体；加载时保留系统/资源装配，但先清空实体。
	a.sim.DestroyAllEntities()
	clear(a.players)
	a.sim.ImportIDs(ecs.IDState{
		Next: sd.Meta.NextEntityId,
		Free: uint64ToEntities(sd.Meta.FreeIds),
	})
	playerID := ecs.ComponentIDOf[components.Player](a.sim)

	for _, es := range sd.Snapshot.Entities {
		e := ecs.Entity(es.EntityId)
		a.sim.CreateEntityWithID(e)
		for _, cs := range es.Components {
			meta, ok := a.sim.Registry().MetaByName(ecs.ComponentID(cs.Component))
			if !ok || meta.Decode == nil || meta.AddTo == nil {
				return fmt.Errorf("world: 存档含未知组件 %q", cs.Component)
			}
			v, err := meta.Decode(cs.Data)
			if err != nil {
				return fmt.Errorf("world: 组件 %s 解码失败: %w", cs.Component, err)
			}
			meta.AddTo(a.sim, e, v)
		}
	}

	a.tick = int64(sd.Meta.Tick)
	a.mapConfig = sd.Map
	if sd.Map != nil {
		regionBiomes := append([]worldmap.BiomeType(nil), sd.RegionBiomes...)
		if len(regionBiomes) == 0 {
			if generated, ok := ecs.TryResource[MapData](a.sim); ok {
				regionBiomes = append(regionBiomes, generated.RegionBiomes...)
			}
		}
		restored := MapData{
			Width:         int(sd.Map.Width),
			Height:        int(sd.Map.Height),
			SpawnX:        int(sd.Map.SpawnX),
			SpawnY:        int(sd.Map.SpawnY),
			CornerHeights: sd.Map.CornerHeights,
			CornerTypes:   sd.Map.CornerTypes,
			TileEffects:   sd.TileEffects,
			TileParams:    bytesToInt8s(sd.TileParams),
			RegionIDs:     sd.TileRegions,
			RegionBiomes:  regionBiomes,
			RegionWeather: weatherBiasFromProto(sd.RegionWeather),
		}
		a.attachMap(&restored)
	}
	// 存档迁移：旧档 Weapon → Attacker；Workable → 受激能力组件（Choppable/Minable/Pickable）；
	// Loot → Lootable；占位物（Block）按模板/建筑语义归一化。
	a.migrateRevivalStatues(sd.Meta.Version)
	a.migrateWeapons()
	a.migrateWorkables()
	a.migrateLoot()
	a.migrateDropSources()
	a.migrateBlockers()
	a.migrateCollides()
	a.migrateBehaviorTrees()
	// 三层全量重建：必须在实体恢复 + MapData 就位 + 迁移之后调用
	//（读档后组件挂载顺序不保证）。
	//   - rebuildBlockers：占位层（放置冲突 + 寻路代价）；
	//   - rebuildCollides：静态形状层；
	//   - SyncDynamicBodies：动态形状层（玩家/动物，按连续位置）。
	rebuildBlockers(a.sim, a.blockers)
	rebuildCollides(a.sim, a.collides)
	systems.SyncDynamicBodies(a.sim)
	if len(sd.Journal) > 0 {
		if err := json.Unmarshal(sd.Journal, &a.journal); err != nil {
			return fmt.Errorf("world: 指令日志解析失败: %w", err)
		}
	}
	if dc := sd.Snapshot.DayCycle; dc != nil {
		cur := ecs.Resource[components.DayCycle](a.sim)
		cur.Phase = int(dc.Phase)
		cur.Light = dc.Light
	}
	if ws := sd.Snapshot.Weather; ws != nil {
		if wr, ok := ecs.TryResource[components.Weather](a.sim); ok {
			wr.Phase = ws.Phase
		}
	}

	// 重建玩家所有权表（实体 → UID）
	for _, es := range sd.Snapshot.Entities {
		for _, cs := range es.Components {
			if ecs.ComponentID(cs.Component) == playerID {
				var p game.Player
				if err := pb.Unmarshal(cs.Data, &p); err == nil {
					a.players[ecs.Entity(es.EntityId)] = p.Uid
				}
			}
		}
	}
	// 兼容旧档：玩家实体补挂 Effects（效果系统覆盖集容器）
	ecs.Query[components.Player](a.sim, func(e ecs.Entity, _ *components.Player) {
		if !ecs.Has[components.Effects](a.sim, e) {
			ecs.Add(a.sim, e, components.Effects{Active: map[components.EffectOrder]components.EffectState{}})
		}
	})

	// 清掉加载产生的脏标记/事件，避免首帧把全量当增量广播
	a.sim.DrainDirtySorted()
	a.sim.DrainEvents()
	return nil
}

// migrateRevivalStatues 为 v1 及更早存档补入地图配置中的复活雕像。
// 已持久化过 Hauntable 的存档不得重新生成，避免把已耗尽的雕像刷回来。
func (a *WorldActor) migrateRevivalStatues(version string) {
	if version != "" && version != "starve-save-v1" {
		return
	}
	if a.config == nil || a.config.MapSpec == nil {
		return
	}
	hasStatue := false
	ecs.Query[components.Hauntable](a.sim, func(_ ecs.Entity, _ *components.Hauntable) {
		hasStatue = true
	})
	if hasStatue {
		return
	}
	seedRevivalStatues(a.sim, a.config.MapSpec.Handplaced.RevivalStatues)
}

// ReplaySave 从存档字节重放指令日志，返回重放后的全量快照。
// 供回放验收工具（cmd/replay）与测试使用：结果应与存档快照一致。
func ReplaySave(data []byte, cfg WorldConfig) (*game.Snapshot, error) {
	var sd game.SaveData
	if err := pb.Unmarshal(data, &sd); err != nil {
		return nil, fmt.Errorf("world: 存档解析失败: %w", err)
	}
	if sd.Meta == nil || len(sd.Journal) == 0 {
		return nil, errors.New("world: 存档缺少元数据或指令日志")
	}
	var entries []JournalEntry
	if err := json.Unmarshal(sd.Journal, &entries); err != nil {
		return nil, fmt.Errorf("world: 指令日志解析失败: %w", err)
	}
	if sd.Meta.MapSeed != 0 {
		cfg.MapSeed = sd.Meta.MapSeed // 重放必须用存档同一种子生成初始世界
	}
	wa := NewWorldActor(cfg)
	wa.Replay(entries, int64(sd.Meta.Tick))
	return FullSnapshot(wa.sim), nil
}

// SaveNow 是事件触发的自动存档便捷入口（如每天开始）：
// 生成存档并经注入的 saveSink 落盘。只在世界 actor goroutine 上调用
// （onTick 检测到事件后调用，触发点预留）。
func (a *WorldActor) SaveNow() {
	if a.saveSink == nil {
		return
	}
	startedAt := time.Now()
	data, err := a.marshalSave()
	if err == nil {
		err = a.saveSink(data)
	}
	a.observeSave(SaveStats{
		Duration: time.Since(startedAt),
		Bytes:    len(data),
		Trigger:  SaveTriggerEvent,
		Err:      err,
	})
}

func (a *WorldActor) observeSave(stats SaveStats) {
	if a.saveObserver != nil {
		a.saveObserver.ObserveSave(stats)
	}
}

// migrateBehaviorTrees 旧档迁移：给"有 AI 但没有行为树"的生物补挂行为树。
//
// 背景：生物决策已从 AISystem 里的硬编码状态机迁移到行为树
// （老的 4 状态 switch 已删除）。旧存档里的生物只有 AI 组件、没有
// BehaviorTree，读档后如果不补挂，AI 就没有决策来源 —— 表现为
// **生物站在原地一动不动**（比崩溃更难发现）。
//
// 树种类沿用生成时的推断规则：能攻击 → 掠食者树，否则被动树。
// 这与 seedCreatures 的规则一致，保证新旧存档行为一致。
func (a *WorldActor) migrateBehaviorTrees() {
	var need []ecs.Entity
	ecs.Query[components.AI](a.sim, func(e ecs.Entity, _ *components.AI) {
		if !ecs.Has[components.BehaviorTree](a.sim, e) {
			need = append(need, e)
		}
	})
	for _, e := range need {
		// 用与生成时相同的规则推断树种类
		systems.EnsureBehaviorTree(a.sim, e, components.TreeKindUnspecified)
	}
}

// migrateWeapons 旧档迁移：Weapon → Attacker（攻击统一走 -er 主动能力）。
func (a *WorldActor) migrateWeapons() {
	var convert []ecs.Entity
	ecs.Query[components.Weapon](a.sim, func(e ecs.Entity, _ *components.Weapon) {
		convert = append(convert, e)
	})
	for _, e := range convert {
		w := ecs.Get[components.Weapon](a.sim, e)
		ecs.Add(a.sim, e, interactive.Attacker{
			AttackDamage:   w.AttackDamage,
			AttackRange:    w.AttackRange,
			AttackCooldown: w.AttackCooldown,
		})
		ecs.Remove[components.Weapon](a.sim, e)
	}
	// 旧档可被攻击实体（玩家/生物）补挂 Attackable；纯 Health 的环境物（植物）不补
	var targets []ecs.Entity
	ecs.Query[components.Player](a.sim, func(e ecs.Entity, _ *components.Player) {
		if !ecs.Has[components.Attackable](a.sim, e) {
			targets = append(targets, e)
		}
	})
	ecs.Query[components.Creature](a.sim, func(e ecs.Entity, _ *components.Creature) {
		if !ecs.Has[components.Attackable](a.sim, e) {
			targets = append(targets, e)
		}
	})
	for _, e := range targets {
		ecs.Add(a.sim, e, components.Attackable{})
	}
}

// migrateWorkables 旧档迁移：Workable{Action,...} → 对应受激能力组件后移除 Workable。
func (a *WorldActor) migrateWorkables() {
	var convert []ecs.Entity
	ecs.Query[components.Workable](a.sim, func(e ecs.Entity, w *components.Workable) {
		if w.WorkLeft > 0 {
			convert = append(convert, e)
		}
	})
	for _, e := range convert {
		w := ecs.Get[components.Workable](a.sim, e)
		switch w.Action {
		case components.WorkChop:
			ecs.Add(a.sim, e, interactive.Choppable{Kind: w.Kind, WorkLeft: w.WorkLeft, MaxWork: w.MaxWork})
		case components.WorkMine:
			ecs.Add(a.sim, e, interactive.Minable{Kind: w.Kind, WorkLeft: w.WorkLeft, MaxWork: w.MaxWork})
		case components.WorkPick:
			ecs.Add(a.sim, e, interactive.Pickable{Kind: w.Kind, WorkLeft: w.WorkLeft, MaxWork: w.MaxWork})
		}
		if t := a.template(w.Kind); t.RespawnTicks > 0 {
			ecs.Add(a.sim, e, components.Respawnable{Ticks: t.RespawnTicks})
		}
		ecs.Remove[components.Workable](a.sim, e)
	}
}

// migrateLoot 旧档迁移：Loot → Lootable（拾取纳入 -er/-able 行为体系）。
func (a *WorldActor) migrateLoot() {
	var convert []ecs.Entity
	ecs.Query[components.Loot](a.sim, func(e ecs.Entity, _ *components.Loot) {
		convert = append(convert, e)
	})
	for _, e := range convert {
		l := ecs.Get[components.Loot](a.sim, e)
		ecs.Add(a.sim, e, components.Lootable{Items: l.Items})
		ecs.Remove[components.Loot](a.sim, e)
	}
}

// migrateDropSources 为独立掉落管线之前的存档补来源。
// 已经是 Lootable 的旧式就地掉落实体不会补挂，避免再次产出。
func (a *WorldActor) migrateDropSources() {
	addResource := func(e ecs.Entity, kind components.ItemKind) {
		if !ecs.Has[components.DropSource](a.sim, e) && !ecs.Has[components.Lootable](a.sim, e) {
			ecs.Add(a.sim, e, components.DropSource{Category: components.DropSourceResource, ResourceKind: kind})
		}
	}
	ecs.Query[interactive.Choppable](a.sim, func(e ecs.Entity, target *interactive.Choppable) {
		addResource(e, target.Kind)
	})
	ecs.Query[interactive.Minable](a.sim, func(e ecs.Entity, target *interactive.Minable) {
		addResource(e, target.Kind)
	})
	ecs.Query[interactive.Pickable](a.sim, func(e ecs.Entity, target *interactive.Pickable) {
		addResource(e, target.Kind)
	})
	ecs.Query[components.Creature](a.sim, func(e ecs.Entity, creature *components.Creature) {
		if ecs.Has[components.DropSource](a.sim, e) || ecs.Has[components.Lootable](a.sim, e) {
			return
		}
		if template := a.config.Creatures[creature.Kind]; len(template.Drops) == 0 && len(creature.Drops) > 0 {
			template.Drops = make([]components.DropRule, 0, len(creature.Drops))
			for _, stack := range creature.Drops {
				if stack.Kind != 0 && stack.Count > 0 {
					template.Drops = append(template.Drops, components.DropRule{
						Kind: stack.Kind, MinCount: stack.Count, MaxCount: stack.Count, Chance: components.DropChanceScale,
					})
				}
			}
			a.config.Creatures[creature.Kind] = template
		}
		ecs.Add(a.sim, e, components.DropSource{Category: components.DropSourceCreature, CreatureKind: creature.Kind})
	})
}

// migrateBlockers 为旧档补挂/纠正**占位（Block）与碰撞体（Collide）**。
//
// 旧档只有 Block（且带 Radius 字段），形状与占位混在一起；新设计拆成两个组件。
// 迁移规则（按模板推导，与 seed.go 保持一致）：
//   - 工作站、复活雕像、已放置建筑：占格盒（Block + Collide(Box)）；
//   - 环境物（Choppable/Minable）：按模板——collision_radius > 0 是格心圆
//     （Collide(Circle) + Block{1,1}），blocking 是整格盒，两者都没有就都不挂；
//   - 所有迁移到的实体补挂 Static（旧档没有运动类别标记）。
//
// 也负责把"旧档里已有 Collide 但缺 Static/Dynamic"的实体补上标记。
// 迁移后由 rebuildBlockers / rebuildCollides 统一重建两层。
func (a *WorldActor) migrateBlockers() {
	setBlock := func(e ecs.Entity, want components.Block) {
		if ecs.Has[components.Block](a.sim, e) {
			if cur := ecs.Get[components.Block](a.sim, e); *cur != want {
				*cur = want
				ecs.MarkDirty[components.Block](a.sim, e)
			}
			return
		}
		ecs.Add(a.sim, e, want)
	}
	// 盒形碰撞体（建筑/工作站/雕像）。这些实体都"推不动"——
	// 靠**没有 Pushable** 表达，不需要额外标记。
	setStaticBox := func(e ecs.Entity, w, h int) {
		setBlock(e, components.Block{Width: w, Height: h})
		if !ecs.Has[components.Collide](a.sim, e) {
			ecs.Add(a.sim, e, components.Collide{Shape: components.CollideShapeBox, Width: w, Height: h})
		}
	}
	ecs.Query[components.Workstation](a.sim, func(e ecs.Entity, _ *components.Workstation) {
		setStaticBox(e, 1, 1)
	})
	ecs.Query[components.Hauntable](a.sim, func(e ecs.Entity, _ *components.Hauntable) {
		setStaticBox(e, 1, 1)
	})
	ecs.Query[components.Building](a.sim, func(e ecs.Entity, b *components.Building) {
		if b.Placed {
			w, h := buildingWH(b)
			setStaticBox(e, w, h)
		}
	})
	syncEnv := func(e ecs.Entity, kind components.ItemKind) {
		tpl := a.template(kind)
		switch {
		case tpl.CollisionRadius > 0:
			setBlock(e, components.Block{Width: 1, Height: 1, Thin: true})
			if !ecs.Has[components.Collide](a.sim, e) {
				ecs.Add(a.sim, e, components.Collide{
					Shape:  components.CollideShapeCircle,
					Radius: tpl.CollisionRadius,
				})
			}
		case tpl.Blocking:
			setStaticBox(e, 1, 1)
		default:
			ecs.Remove[components.Block](a.sim, e)
			ecs.Remove[components.Collide](a.sim, e)
		}
	}
	ecs.Query[interactive.Choppable](a.sim, func(e ecs.Entity, c *interactive.Choppable) {
		syncEnv(e, c.Kind)
	})
	ecs.Query[interactive.Minable](a.sim, func(e ecs.Entity, m *interactive.Minable) {
		syncEnv(e, m.Kind)
	})
}

func entitiesToUint64(es []ecs.Entity) []uint64 {
	out := make([]uint64, 0, len(es))
	for _, e := range es {
		out = append(out, uint64(e))
	}
	return out
}

func uint64ToEntities(us []uint64) []ecs.Entity {
	out := make([]ecs.Entity, 0, len(us))
	for _, u := range us {
		out = append(out, ecs.Entity(u))
	}
	return out
}

func int8sToBytes(v []int8) []byte {
	out := make([]byte, len(v))
	for i, b := range v {
		out[i] = byte(b)
	}
	return out
}

func bytesToInt8s(v []byte) []int8 {
	out := make([]int8, len(v))
	for i, b := range v {
		out[i] = int8(b)
	}
	return out
}

func weatherBiasToProto(v []WeatherBias) []*game.WeatherBias {
	out := make([]*game.WeatherBias, 0, len(v))
	for _, b := range v {
		out = append(out, &game.WeatherBias{Temp: b.Temp, Fog: b.Fog, Rain: b.Rain})
	}
	return out
}

func weatherBiasFromProto(v []*game.WeatherBias) []WeatherBias {
	out := make([]WeatherBias, 0, len(v))
	for _, b := range v {
		if b != nil {
			out = append(out, WeatherBias{Temp: b.Temp, Fog: b.Fog, Rain: b.Rain})
		}
	}
	return out
}

// migrateCollides 为旧档补挂缺失的 Collide。
//
// 历史背景：最初"碰撞形状"和"占位"混在 Block 里（Block.Radius），后来拆成
// 独立的 Collide。旧档里没有 Collide，需要按实体身份补一个。
//
// 注意这里**不再涉及 Static/Dynamic**：那两个 tag 已被删除。
//   - "会不会自己动" = 有没有 Moveable（索引动态层与 ORCA 邻居表的判据）；
//   - "推不推得动"   = 有没有 Pushable。
//
// 两者都由组件组合直接表达，读档不需要为它们做迁移。
func (a *WorldActor) migrateCollides() {
	// 会自己动的实体（玩家/生物）：形状是胶囊，参数从生物模板取。
	ecs.Query2[components.Moveable, components.Position](a.sim, func(e ecs.Entity, _ *components.Moveable, _ *components.Position) {
		if ecs.Has[components.Collide](a.sim, e) {
			return
		}
		col := components.Collide{
			Shape:      components.CollideShapeCapsule,
			Radius:     systems.BodyRadius,
			BodyHeight: systems.BodyHeight,
		}
		if cr := ecs.Get[components.Creature](a.sim, e); cr != nil {
			if tpl, ok := a.config.Creatures[cr.Kind]; ok {
				col.Radius = tpl.BodyRadius
				col.HalfLength = tpl.BodyHalfLength
				col.BodyHeight = tpl.BodyHeight
			}
		}
		ecs.Add(a.sim, e, col)
	})
	// 不自己动但占位的实体：按 Block 的语义补形状。
	//   - Thin（树/岩的格心圆）：半径从模板恢复（旧档丢了半径）；
	//   - 其余（建筑/工作站/雕像）：占格盒。
	ecs.Query2[components.Block, components.Position](a.sim, func(e ecs.Entity, b *components.Block, _ *components.Position) {
		if ecs.Has[components.Collide](a.sim, e) {
			return
		}
		if b.Thin {
			ecs.Add(a.sim, e, components.Collide{
				Shape:  components.CollideShapeCircle,
				Radius: collideRadiusFromTemplate(a, e),
			})
			return
		}
		w, h := b.Width, b.Height
		if w <= 0 {
			w = 1
		}
		if h <= 0 {
			h = 1
		}
		ecs.Add(a.sim, e, components.Collide{
			Shape: components.CollideShapeBox, Width: w, Height: h,
		})
	})
}

// collideRadiusFromTemplate 按实体的环境物模板取碰撞半径。
// 旧档的半径已随 Block.Radius 删除，只能从模板恢复（树/岩的配置值）。
func collideRadiusFromTemplate(a *WorldActor, e ecs.Entity) float64 {
	if ch := ecs.Get[interactive.Choppable](a.sim, e); ch != nil {
		return a.template(ch.Kind).CollisionRadius
	}
	if mi := ecs.Get[interactive.Minable](a.sim, e); mi != nil {
		return a.template(mi.Kind).CollisionRadius
	}
	return 0.18 // 兜底：树/岩的典型量级
}
