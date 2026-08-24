package world

import (
	"sort"
	"time"

	pb "google.golang.org/protobuf/proto"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/config"
	"starve/pkg/proto"
	game "starve/pkg/proto/game"
)

func (a *WorldActor) viewRadius() int {
	return config.NormalizeViewRadius(a.cfg.ViewRadius)
}

func (a *WorldActor) interestRadius() int {
	return config.InterestRadius(a.cfg.ViewRadius, a.cfg.ViewRadiusMax, a.cfg.ViewPreload)
}

func (a *WorldActor) viewSnapshot(uid string) *game.Snapshot {
	if uid == "" {
		return FullSnapshot(a.sim)
	}
	viewer, ok := a.findPlayer(uid)
	if !ok {
		return FullSnapshot(a.sim)
	}
	visible := a.collectVisible(viewer)
	a.rememberInterest(viewer, visible)
	snap := &game.Snapshot{
		Entities: encodeEntities(a.sim, sortedEntityList(visible)),
		DayCycle: dayCycleOf(a.sim),
		Weather:  weatherOf(a.sim),
	}
	return snap
}

type interestPushStats struct {
	bytes  int
	scan   time.Duration
	encode time.Duration
}

func (a *WorldActor) pushInterestDeltas(
	dirty []ecs.DirtyEntry,
	removed []ecs.Entity,
	events []*game.WorldEvent,
) interestPushStats {
	viewers := a.onlineViewers()
	if len(viewers) == 0 {
		started := time.Now()
		delta := DeltaSnapshot(a.sim, dirty, removed)
		delta.Tick = uint64(a.tick)
		delta.Events = events
		a.outbox = append(a.outbox, PushEffect{
			Route:     proto.RouteSnapshotDelta,
			Payload:   delta,
			WorldTick: uint64(a.tick),
			InputAcks: a.cloneInputAcks(),
		})
		return interestPushStats{bytes: pb.Size(delta), encode: time.Since(started)}
	}
	dirtyByEntity := make(map[ecs.Entity]ecs.DirtyEntry, len(dirty))
	for _, d := range dirty {
		dirtyByEntity[d.Entity] = d
	}
	removedSet := make(map[ecs.Entity]struct{}, len(removed))
	for _, e := range removed {
		removedSet[e] = struct{}{}
	}
	var stats interestPushStats
	acks := a.cloneInputAcks()
	scanStart := time.Now()
	visibleBy := a.collectAllVisible(viewers)
	stats.scan = time.Since(scanStart)
	for _, viewer := range viewers {
		visible := visibleBy[viewer]
		last := a.interest[viewer]
		encodeStart := time.Now()
		delta := projectViewDelta(a.sim, viewer, last, visible, dirtyByEntity, removedSet, events)
		delta.Tick = uint64(a.tick)
		stats.encode += time.Since(encodeStart)
		a.rememberInterest(viewer, visible)
		a.outbox = append(a.outbox, PushEffect{
			UID:       a.players[viewer],
			Route:     proto.RouteSnapshotDelta,
			Payload:   delta,
			WorldTick: uint64(a.tick),
			InputAcks: acks,
		})
		stats.bytes += pb.Size(delta)
	}
	return stats
}

func (a *WorldActor) onlineViewers() []ecs.Entity {
	out := make([]ecs.Entity, 0, len(a.players))
	for e := range a.players {
		if !a.sim.IsAlive(e) || ecs.Has[components.Offline](a.sim, e) {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (a *WorldActor) viewerOrigin(viewer ecs.Entity) (ox, oy int, ok bool) {
	if ecs.Has[components.Position](a.sim, viewer) {
		p := ecs.Get[components.Position](a.sim, viewer)
		return p.X, p.Y, true
	}
	return 0, 0, false
}

func (a *WorldActor) attachViewerExtras(viewer ecs.Entity, seen map[ecs.Entity]struct{}) {
	if a.sim.IsAlive(viewer) {
		seen[viewer] = struct{}{}
	}
	if ecs.Has[components.Equip](a.sim, viewer) {
		eq := ecs.Get[components.Equip](a.sim, viewer)
		for _, slot := range components.All() {
			if item := eq.Item(slot); item != 0 && a.sim.IsAlive(item) {
				seen[item] = struct{}{}
			}
		}
	}
}

func (a *WorldActor) collectVisible(viewer ecs.Entity) map[ecs.Entity]struct{} {
	return a.collectAllVisible([]ecs.Entity{viewer})[viewer]
}

func (a *WorldActor) collectAllVisible(viewers []ecs.Entity) map[ecs.Entity]map[ecs.Entity]struct{} {
	radius := a.interestRadius()
	type origin struct {
		x, y int
		ok   bool
	}
	origins := make([]origin, len(viewers))
	out := make(map[ecs.Entity]map[ecs.Entity]struct{}, len(viewers))
	for i, v := range viewers {
		seen := make(map[ecs.Entity]struct{})
		a.attachViewerExtras(v, seen)
		origins[i].x, origins[i].y, origins[i].ok = a.viewerOrigin(v)
		out[v] = seen
	}
	// 只扫一遍 Position；每个实体在这一遍里对所有在线玩家做切比雪夫。
	ecs.Query[components.Position](a.sim, func(e ecs.Entity, p *components.Position) {
		for i, v := range viewers {
			o := origins[i]
			if !o.ok || inViewSquare(p.X, p.Y, o.x, o.y, radius) {
				out[v][e] = struct{}{}
			}
		}
	})
	var buildings []ecs.Entity
	ecs.Query[components.Building](a.sim, func(e ecs.Entity, b *components.Building) {
		if !b.Placed && !ecs.Has[components.Position](a.sim, e) {
			buildings = append(buildings, e)
		}
	})
	for _, v := range viewers {
		for _, e := range buildings {
			out[v][e] = struct{}{}
		}
	}
	return out
}

func (a *WorldActor) rememberInterest(viewer ecs.Entity, visible map[ecs.Entity]struct{}) {
	if a.interest == nil {
		a.interest = make(map[ecs.Entity]map[ecs.Entity]struct{})
	}
	cp := make(map[ecs.Entity]struct{}, len(visible))
	for e := range visible {
		cp[e] = struct{}{}
	}
	a.interest[viewer] = cp
}

func (a *WorldActor) forgetInterest(viewer ecs.Entity) {
	delete(a.interest, viewer)
}

func projectViewDelta(
	sim *ecs.World,
	viewer ecs.Entity,
	last, visible map[ecs.Entity]struct{},
	dirtyByEntity map[ecs.Entity]ecs.DirtyEntry,
	removedSet map[ecs.Entity]struct{},
	events []*game.WorldEvent,
) *game.SnapshotDelta {
	if last == nil {
		last = map[ecs.Entity]struct{}{}
	}
	var entered, stayDirty []ecs.Entity
	for e := range visible {
		if _, ok := last[e]; !ok {
			entered = append(entered, e)
			continue
		}
		if _, ok := dirtyByEntity[e]; ok {
			stayDirty = append(stayDirty, e)
		}
	}
	removed := make(map[ecs.Entity]struct{})
	for e := range last {
		if _, still := visible[e]; !still {
			removed[e] = struct{}{}
		}
	}
	for e := range removedSet {
		if _, ok := last[e]; ok {
			removed[e] = struct{}{}
		}
	}

	stayDirtySet := make(map[ecs.Entity]struct{}, len(stayDirty))
	var stayEntries []ecs.DirtyEntry
	for _, e := range stayDirty {
		stayDirtySet[e] = struct{}{}
		stayEntries = append(stayEntries, dirtyByEntity[e])
	}
	delta := DeltaSnapshot(sim, stayEntries, nil)
	if len(entered) > 0 {
		enteredStates := encodeEntities(sim, entered)
		merged := make(map[ecs.Entity]*game.EntityState, len(delta.Entities)+len(enteredStates))
		for _, st := range delta.Entities {
			merged[ecs.Entity(st.EntityId)] = st
		}
		for _, st := range enteredStates {
			merged[ecs.Entity(st.EntityId)] = st
		}
		delta.Entities = sortedStates(merged)
	}
	if len(delta.RemovedComponents) > 0 {
		kept := delta.RemovedComponents[:0]
		for _, rc := range delta.RemovedComponents {
			if _, ok := stayDirtySet[ecs.Entity(rc.EntityId)]; ok {
				kept = append(kept, rc)
			}
		}
		delta.RemovedComponents = kept
	}
	delta.RemovedEntities = uint64Entities(sortedEntityList(removed))
	delta.Events = filterViewEvents(events, visible, viewer)
	delta.DayCycle = dayCycleOf(sim)
	delta.Weather = weatherOf(sim)
	return delta
}

func filterViewEvents(events []*game.WorldEvent, visible map[ecs.Entity]struct{}, viewer ecs.Entity) []*game.WorldEvent {
	if len(events) == 0 {
		return nil
	}
	out := make([]*game.WorldEvent, 0, len(events))
	for _, ev := range events {
		if eventVisible(ev, visible, viewer) {
			out = append(out, ev)
		}
	}
	return out
}

func eventVisible(ev *game.WorldEvent, visible map[ecs.Entity]struct{}, viewer ecs.Entity) bool {
	for _, e := range eventEntities(ev) {
		if e == viewer {
			return true
		}
		if _, ok := visible[e]; ok {
			return true
		}
	}
	return false
}

func eventEntities(ev *game.WorldEvent) []ecs.Entity {
	if ev == nil {
		return nil
	}
	switch p := ev.Payload.(type) {
	case *game.WorldEvent_Impact:
		if p.Impact == nil {
			return nil
		}
		return []ecs.Entity{ecs.Entity(p.Impact.SourceEntity), ecs.Entity(p.Impact.TargetEntity)}
	case *game.WorldEvent_HealthChanged:
		if p.HealthChanged == nil {
			return nil
		}
		return []ecs.Entity{ecs.Entity(p.HealthChanged.TargetEntity), ecs.Entity(p.HealthChanged.SourceEntity)}
	case *game.WorldEvent_Outcome:
		if p.Outcome == nil {
			return nil
		}
		return []ecs.Entity{ecs.Entity(p.Outcome.EntityId)}
	default:
		return nil
	}
}

func inViewSquare(x, y, cx, cy, radius int) bool {
	if radius < 0 {
		return true
	}
	return absInt(x-cx) <= radius && absInt(y-cy) <= radius
}

func sortedEntityList(set map[ecs.Entity]struct{}) []ecs.Entity {
	if len(set) == 0 {
		return nil
	}
	out := make([]ecs.Entity, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func uint64Entities(ids []ecs.Entity) []uint64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]uint64, len(ids))
	for i, id := range ids {
		out[i] = uint64(id)
	}
	return out
}
