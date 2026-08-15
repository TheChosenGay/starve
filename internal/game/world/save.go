package world

import (
	"encoding/json"
	"errors"
	"fmt"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	game "starve/pkg/proto/game"
)

// SaveVersion 存档格式版本（未来兼容演进）。
const SaveVersion = "starve-save-v1"

// SaveRequest 请求保存：返回存档字节（请求-应答）。
// 保存统一走 actor 消息（线性模型）：客户端点存档、关服保存都经此入口。
type SaveRequest struct{}

// Save 导出世界为存档字节（实体+组件快照 + 世界元数据）。
// 只能在世界 actor goroutine 上调用（SaveRequest 或 onTick 内），保证线性。
func (a *WorldActor) Save() []byte {
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
	}
	b, err := pb.Marshal(data)
	if err != nil {
		return nil
	}
	return b
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
		a.sim.AddResource(&MapData{
			Width:         int(sd.Map.Width),
			Height:        int(sd.Map.Height),
			SpawnX:        int(sd.Map.SpawnX),
			SpawnY:        int(sd.Map.SpawnY),
			CornerHeights: sd.Map.CornerHeights,
			CornerTypes:   sd.Map.CornerTypes,
			TileEffects:   sd.TileEffects,
			TileParams:    bytesToInt8s(sd.TileParams),
			RegionIDs:     sd.TileRegions,
			RegionWeather: weatherBiasFromProto(sd.RegionWeather),
		})
	}
	// 存档迁移：旧档 Weapon → Attacker；Workable → 受激能力组件（Choppable/Minable/Pickable）；
	// Block 机制之前的旧档没有 Block——已放置建筑 + 阻挡类环境物补挂 Block。
	a.migrateWeapons()
	a.migrateWorkables()
	a.migrateBlocks()
	// 动态阻挡层重建：地形即时推导，Block 实体（建筑/树/岩）重写阻挡层。
	// 必须在实体恢复 + MapData 就位 + 迁移之后调用。
	rebuildBlocks(a.sim)
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
	if a.saveSink != nil {
		a.saveSink(a.Save())
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

// migrateBlocks 为旧档补挂 Block：已放置建筑 + 模板标记 blocking 的环境物。
// 迁移后由 rebuildBlocks 统一写入 MapData 阻挡层。
func (a *WorldActor) migrateBlocks() {
	ecs.Query[components.Building](a.sim, func(e ecs.Entity, b *components.Building) {
		if b.Placed && !ecs.Has[components.Block](a.sim, e) {
			w, h := buildingWH(b)
			ecs.Add(a.sim, e, components.Block{Width: w, Height: h})
		}
	})
	ecs.Query[interactive.Choppable](a.sim, func(e ecs.Entity, c *interactive.Choppable) {
		if !ecs.Has[components.Block](a.sim, e) && a.template(c.Kind).Blocking {
			ecs.Add(a.sim, e, components.Block{Width: 1, Height: 1})
		}
	})
	ecs.Query[interactive.Minable](a.sim, func(e ecs.Entity, m *interactive.Minable) {
		if !ecs.Has[components.Block](a.sim, e) && a.template(m.Kind).Blocking {
			ecs.Add(a.sim, e, components.Block{Width: 1, Height: 1})
		}
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
