package ecs

import (
	"fmt"
	"math/bits"
	"reflect"
)

// storageLike 是所有组件存储（*sparseSet[T]）实现的统一接口，
// 供 World.DestroyEntity 批量清理与 dirty 标记。
type storageLike interface {
	removeEntity(e Entity)
	hasEntity(e Entity) bool
	componentID() ComponentID
}

// World 是 ECS 模拟内核：实体分配、组件存储（稀疏集）、系统调度、
// 资源与事件/dirty 的持有者。
//
// 确定性纪律（设计文档 §4.4）：
//   - 迭代一律走 Query；内部 map 只做集合/查表，不做遍历
//   - 随机数/时钟由 Resource 提供，禁止全局 rand 与 time.Now()
//   - 实体 ID 密集分配 + 空闲列表复用
//
// 并发：World **非并发安全**，必须由单一所有者 goroutine（world actor）
// 串行访问。actor 邮箱是唯一入口边界，ECS 内部不加锁。
// 误共享会触发数据竞争——测试统一用 `go test -race` 兜底。
type World struct {
	nextID  uint64
	freeIDs []Entity
	alive   map[Entity]struct{}

	// storages：组件类型 → 该类型的稀疏集（实际是 *sparseSet[T]，异质存储所以用 any）。
	// 键是 reflect.Type（组件的 Go 类型本身），首次 Add/Query 时惰性创建，见 storage()。
	storages map[reflect.Type]any
	// storageOrder：存储创建顺序（首次使用某组件类型的顺序）。DestroyEntity 遍历用它，
	// 保证生命周期钩子触发顺序确定性（map 遍历顺序是随机的）。
	storageOrder []reflect.Type
	// masks：每个实体的组件掩码（bit = 存储创建顺序位号，见 sparseSet.maskBit）。
	// 平铺存储：实体 e 的掩码 = masks[e*maskWords : (e+1)*maskWords]；
	// 组件类型超过 64 时 maskWords 自动扩容（growMaskWords，启动期一次性迁移）。
	// 实体 ID 索引，只增不减；销毁时清位。DestroyEntity 只遍历实体实际拥有的组件。
	maskWords int
	masks     []uint64

	resources map[reflect.Type]any // 资源类型 → 注入的资源指针（*T）
	systems   []systemEntry        // 固定顺序的系统列表

	registry *ComponentRegistry

	events  []Event   // 结构变更事件队列，调用方在 tick 边界 DrainEvents() 消费
	effects []any     // 副作用队列（组件发射的通知等），调用方在 tick 边界 DrainEffects() 翻译成推送
	dirty   []dirtyOp // 脏标记，调用方 DrainDirty() / DrainDirtySorted() 消费
}

func NewWorld() *World {
	return &World{
		nextID:       uint64(NullEntity), // 0 保留，ID 从 1 开始
		alive:        make(map[Entity]struct{}),
		storages:     make(map[reflect.Type]any),
		storageOrder: nil,
		maskWords:    1,
		masks:        nil,
		resources:    make(map[reflect.Type]any),
		registry:     NewComponentRegistry(),
	}
}

// ensureMask 保证实体 ID 有对应的掩码槽（按需翻倍扩容，只增不减）。
func (w *World) ensureMask(e Entity) {
	need := (int(e) + 1) * w.maskWords
	if need <= len(w.masks) {
		return
	}
	size := max(need, len(w.masks)*2)
	grown := make([]uint64, size)
	copy(grown, w.masks)
	w.masks = grown
}

// growMaskWords 组件类型跨越 64 边界时扩掩码宽度（迁移全部实体掩码）。
// 组件在启动期注册完毕，正常只触发一次；迁移 O(实体数 × 旧字数)。
func (w *World) growMaskWords(words int) {
	if words <= w.maskWords {
		return
	}
	slots := len(w.masks) / w.maskWords
	grown := make([]uint64, slots*words)
	for i := 0; i < slots; i++ {
		copy(grown[i*words:], w.masks[i*w.maskWords:(i+1)*w.maskWords])
	}
	w.masks = grown
	w.maskWords = words
}

// maskAt 返回实体 e 的掩码字（maskWords 个 uint64）。
func (w *World) maskAt(e Entity) []uint64 {
	base := int(e) * w.maskWords
	return w.masks[base : base+w.maskWords]
}

// maskSet 置位/清位实体的某个组件位（位号 = 存储创建顺序）；跨界自动扩容。
func (w *World) maskSet(e Entity, bit uint, on bool) {
	if words := int(bit/64) + 1; words > w.maskWords {
		w.growMaskWords(words)
	}
	w.ensureMask(e)
	m := w.maskAt(e)
	word, pos := bit/64, bit%64
	if on {
		m[word] |= 1 << pos
	} else {
		m[word] &^= 1 << pos
	}
}

// storage 返回组件 T 的稀疏集，首次使用时惰性创建（零注册仪式）。
func storage[T any](w *World) *sparseSet[T] {
	t := reflect.TypeOf((*T)(nil)).Elem()
	st, ok := w.storages[t]
	if !ok {
		w.registry.ensure(t)
		s := newSparseSet[T]()
		s.compID = w.registry.Name(t)
		s.maskBit = uint(len(w.storageOrder))
		w.storages[t] = s
		w.storageOrder = append(w.storageOrder, t)
		st = s
	}
	return st.(*sparseSet[T])
}

// CreateEntity 分配一个新实体 ID：优先复用空闲列表，否则密集递增。
func (w *World) CreateEntity() Entity {
	var e Entity
	if n := len(w.freeIDs); n > 0 {
		e = w.freeIDs[n-1]
		w.freeIDs = w.freeIDs[:n-1]
	} else {
		w.nextID++
		e = Entity(w.nextID)
	}
	w.alive[e] = struct{}{}
	w.ensureMask(e)
	w.events = append(w.events, Event{Kind: EntityCreated, Entity: e})
	return e
}

// DestroyEntity 销毁实体：清掉其所有组件、回收 ID，并触发事件。
// 重复销毁会 panic（编程错误，尽早暴露）。
func (w *World) DestroyEntity(e Entity) {
	w.requireAlive(e)
	mask := w.maskAt(e)
	// 第一遍：实体完整时先触发组件的移除钩子（OnRemove 需要能读到其他组件）。
	// 只遍历实体实际拥有的组件（掩码置位），位号升序 = 存储创建顺序，确定性。
	for wi, word := range mask {
		for m := word; m != 0; m &= m - 1 {
			st := w.storages[w.storageOrder[wi*64+int(bits.TrailingZeros64(m))]]
			if ls, ok := st.(lifecycleRemover); ok {
				ls.lifecycleRemove(w, e)
			}
		}
	}
	// 第二遍：批量清组件、回收 ID。
	for wi, word := range mask {
		for m := word; m != 0; m &= m - 1 {
			st := w.storages[w.storageOrder[wi*64+int(bits.TrailingZeros64(m))]]
			s := st.(storageLike)
			if s.hasEntity(e) {
				w.markDirty(e, s.componentID())
			}
			s.removeEntity(e)
		}
	}
	for i := range mask {
		mask[i] = 0
	}
	delete(w.alive, e)
	w.freeIDs = append(w.freeIDs, e)
	w.events = append(w.events, Event{Kind: EntityDestroyed, Entity: e})
}

// IsAlive 判断实体是否存活。
func (w *World) IsAlive(e Entity) bool {
	_, ok := w.alive[e]
	return ok
}

// EntityCount 返回当前存活实体数。
func (w *World) EntityCount() int { return len(w.alive) }

// DestroyAllEntities 按 ID 升序清空实体；用于在同一已装配 World 上加载存档。
func (w *World) DestroyAllEntities() {
	for id := uint64(1); id <= w.nextID; id++ {
		e := Entity(id)
		if w.IsAlive(e) {
			w.DestroyEntity(e)
		}
	}
}

func (w *World) requireAlive(e Entity) {
	if !w.IsAlive(e) {
		panic(fmt.Sprintf("ecs: entity %d is not alive", e))
	}
}

// IDState 是实体 ID 分配状态（存档导出/恢复用）。
type IDState struct {
	Next uint64
	Free []Entity
}

// ExportIDs 导出实体 ID 分配状态（存档用）。
func (w *World) ExportIDs() IDState {
	return IDState{
		Next: w.nextID,
		Free: append([]Entity(nil), w.freeIDs...),
	}
}

// ImportIDs 恢复实体 ID 分配状态（加载存档用）。
func (w *World) ImportIDs(s IDState) {
	w.nextID = s.Next
	w.freeIDs = append(w.freeIDs[:0], s.Free...)
}

// CreateEntityWithID 按指定 ID 创建实体（加载存档用，不分配新 ID）。
// ID 必须未被占用，否则 panic（存档损坏）。
func (w *World) CreateEntityWithID(id Entity) {
	if _, ok := w.alive[id]; ok {
		panic(fmt.Sprintf("ecs: entity %d already exists", id))
	}
	w.alive[id] = struct{}{}
	w.ensureMask(id)
	w.events = append(w.events, Event{Kind: EntityCreated, Entity: id})
}
