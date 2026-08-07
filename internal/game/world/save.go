package world

import (
	"errors"
	"fmt"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

// SaveVersion 存档格式版本（未来兼容演进）。
const SaveVersion = "starve-save-v1"

// SaveRequest 请求保存：返回存档字节（请求-应答，供外部/关服触发）。
type SaveRequest struct{}

// Save 导出世界为存档字节（实体+组件快照 + 世界元数据）。
func (a *WorldActor) Save() []byte {
	ids := a.sim.ExportIDs()
	data := &game.SaveData{
		Snapshot: FullSnapshot(a.sim),
		Meta: &game.WorldMeta{
			Tick:         uint64(a.tick),
			NextEntityId: ids.Next,
			FreeIds:      entitiesToUint64(ids.Free),
			Version:      SaveVersion,
		},
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
	if dc := sd.Snapshot.DayCycle; dc != nil {
		cur := ecs.Resource[components.DayCycle](a.sim)
		cur.Phase = int(dc.Phase)
		cur.Light = dc.Light
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

	// 清掉加载产生的脏标记/事件，避免首帧把全量当增量广播
	a.sim.DrainDirtySorted()
	a.sim.DrainEvents()
	return nil
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
