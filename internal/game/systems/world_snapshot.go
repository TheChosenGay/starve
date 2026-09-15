package systems

import (
	"starve/internal/ecs"
	"starve/internal/game/collision"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
)

// WorldSnapshot 是一份"够用于移动解算"的世界最小镜像。
//
// 用途：让**不持有服务端 ECS 世界**的调用方（TUI、将来的客户端回放）也能调用
// 与服务端完全相同的 MoveSolver —— 这才是"同一份代码"的真正含义：
// 不是把公式抄一遍，而是把服务端求解器原样搬过来跑。
//
// 只镜像三样东西：碰撞索引（静态形状 + 动态体）、实体的 Position/Moveable/Collide。
// 地形可走性由调用方通过 SetWalkable 提供（TUI 从地图配置读，服务端就在 MapData 里）。
type WorldSnapshot struct {
	World *ecs.World
	Index *collision.Index

	// pos/mv/col 是实体 → 组件值的映射（快照是只读的，不需要走 ECS 组件存储）。
	pos map[ecs.Entity]components.Position
	mv  map[ecs.Entity]components.Moveable
	col map[ecs.Entity]components.Collide
	// dynamic 标记哪些实体是动态体（进碰撞索引的动态侧）。
	dynamic map[ecs.Entity]struct{}
}

// NewWorldSnapshot 建一份空快照。
func NewWorldSnapshot() *WorldSnapshot {
	w := ecs.NewWorld()
	// 组件 codec 必须先注册：下面要用 ecs.Add 写组件。
	components.RegisterCodecs(w, false)
	idx := collision.NewIndex()
	w.AddResource(idx)
	return &WorldSnapshot{
		World:   w,
		Index:   idx,
		pos:     make(map[ecs.Entity]components.Position),
		mv:      make(map[ecs.Entity]components.Moveable),
		col:     make(map[ecs.Entity]components.Collide),
		dynamic: make(map[ecs.Entity]struct{}),
	}
}

// SetMap 挂上地形数据：MoveSolver 的格子层（水/悬崖判定）会读它的 Walkable。
// 传 nil 相当于"全可走"（纯碰撞测试用）。
//
// 幂等：已挂过就地覆盖内容（AddResource 对重复资源会 panic，而 Sync 每帧都会调）。
func (s *WorldSnapshot) SetMap(md *worldmap.MapData) {
	if md == nil {
		return
	}
	if cur, ok := ecs.TryResource[worldmap.MapData](s.World); ok {
		*cur = *md
		return
	}
	s.World.AddResource(md)
}

// AddStatic 加一个静态碰撞体（树/岩/建筑）。
func (s *WorldSnapshot) AddStatic(e ecs.Entity, col components.Collide, p components.Position) {
	s.col[e] = col
	s.pos[e] = p
	s.applyCollide(e, col, p, false)
}

// AddDynamic 加一个动态实体（玩家/动物）：写入 Position/Moveable/Collide 并登记到碰撞索引。
//
// 幂等：实体已存在时走 Set 更新而不是 Add——每次收到快照 Sync 都会重建动态体，
// 若这里用 Add 会在第二次调用时 panic（"entity already has component"）。
func (s *WorldSnapshot) AddDynamic(
	e ecs.Entity, col components.Collide, p components.Position, mv components.Moveable,
) {
	s.col[e] = col
	s.pos[e] = p
	s.mv[e] = mv
	s.dynamic[e] = struct{}{}
	// 在真实 ecs.World 里落一份组件，MoveSolver 会通过 ecs.Has/Get 读取。
	// 实体可能还不存在（客户端按快照 id 建），先确保它被创建。
	if !s.World.IsAlive(e) {
		s.World.CreateEntityWithID(e)
	}
	upsert(s.World, e, p)
	upsert(s.World, e, mv)
	upsert(s.World, e, col)
	s.applyCollide(e, col, p, true)
}

// upsert 组件存在则更新、不存在则添加（避免 ecs.Add 的重复添加 panic）。
func upsert[T any](w *ecs.World, e ecs.Entity, c T) {
	if ecs.Has[T](w, e) {
		ecs.Set(w, e, c)
		return
	}
	ecs.Add(w, e, c)
}

// UpdateDynamic 更新一个动态实体的位置与速度（每 tick 由调用方同步）。
func (s *WorldSnapshot) UpdateDynamic(e ecs.Entity, p components.Position, mv components.Moveable) {
	if _, ok := s.dynamic[e]; !ok {
		return
	}
	s.pos[e], s.mv[e] = p, mv
	ecs.Set(s.World, e, p)
	ecs.Set(s.World, e, mv)
	col := s.col[e]
	s.applyCollide(e, col, p, true)
}

// Remove 移除一个实体（连同它的碰撞形状）。
func (s *WorldSnapshot) Remove(e ecs.Entity) {
	delete(s.pos, e)
	delete(s.mv, e)
	delete(s.col, e)
	if _, ok := s.dynamic[e]; ok {
		delete(s.dynamic, e)
		s.Index.ClearDynamic(e)
		return
	}
	s.Index.Clear(e)
}

// RemoveAllDynamic 清空全部动态体（每次收到快照后重建用）。
//
// 同时销毁 ecs.World 里的实体：否则残留的 Moveable/Collide 会让下一次
// AddDynamic 走到"实体已存在且有组件"的路径，也容易让 MoveSolver 读到过期成员。
func (s *WorldSnapshot) RemoveAllDynamic() {
	for e := range s.dynamic {
		delete(s.pos, e)
		delete(s.mv, e)
		delete(s.col, e)
		s.Index.ClearDynamic(e)
		if s.World.IsAlive(e) {
			s.World.DestroyEntity(e)
		}
	}
	clear(s.dynamic)
}

// applyCollide 把形状写进碰撞索引（静态走 Set/SetBox，动态走 SetDynamic）。
func (s *WorldSnapshot) applyCollide(e ecs.Entity, col components.Collide, p components.Position, dynamic bool) {
	switch {
	case dynamic:
		s.Index.SetDynamic(e, float64(p.X), float64(p.Y),
			col.Radius, col.HalfLength, float64(col.FaceX), float64(col.FaceZ))
	case col.Shape == components.CollideShapeCircle:
		s.Index.Set(e, float64(p.X)+0.5, float64(p.Y)+0.5, col.Radius)
	case col.Shape == components.CollideShapeBox:
		s.Index.SetBox(e, float64(p.X), float64(p.Y), float64(col.Width), float64(col.Height))
	case col.Shape == components.CollideShapeCapsule:
		s.Index.SetDynamic(e, float64(p.X), float64(p.Y),
			col.Radius, col.HalfLength, float64(col.FaceX), float64(col.FaceZ))
	}
}
