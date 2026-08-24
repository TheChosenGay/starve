package world

import (
	"testing"
	"time"

	"starve/internal/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
	game "starve/pkg/proto/game"
)

func placeProp(t *testing.T, wa *WorldActor, x, y int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Growable{Stage: 1})
	return e
}

func syncTick(t *testing.T, eng *actor.Engine, pid *actor.PID) {
	t.Helper()
	eng.Send(pid, Tick{})
	resp := eng.Request(pid, QueryWorldTime{}, time.Second)
	if _, err := resp.Wait(); err != nil {
		t.Fatal(err)
	}
}

func lastDeltaFor(pushed []PushEffect, uid string) *game.SnapshotDelta {
	var latest *game.SnapshotDelta
	for i := range pushed {
		ef := pushed[i]
		d, ok := ef.Payload.(*game.SnapshotDelta)
		if !ok {
			continue
		}
		if uid == "" || ef.UID == uid || ef.UID == "" {
			latest = d
		}
	}
	return latest
}

func deltaHasEntity(d *game.SnapshotDelta, e ecs.Entity) bool {
	if d == nil {
		return false
	}
	for _, st := range d.Entities {
		if st.EntityId == uint64(e) {
			return true
		}
	}
	return false
}

func deltaRemoved(d *game.SnapshotDelta, e ecs.Entity) bool {
	if d == nil {
		return false
	}
	for _, id := range d.RemovedEntities {
		if id == uint64(e) {
			return true
		}
	}
	return false
}

func clipView(r int) WorldConfig {
	return WorldConfig{ViewRadius: r, ViewPreload: -1}
}

func TestViewSnapshotOmitsFarEntities(t *testing.T) {
	wa := NewWorldActor(clipView(2))
	player := wa.createPlayer("u1")
	near := placeProp(t, wa, 2, 0)
	far := placeProp(t, wa, 5, 0)

	snap := wa.viewSnapshot("u1")
	ids := map[uint64]bool{}
	for _, st := range snap.Entities {
		ids[st.EntityId] = true
	}
	if !ids[uint64(player)] || !ids[uint64(near)] {
		t.Fatalf("基线应含玩家和近处实体: %v", ids)
	}
	if ids[uint64(far)] {
		t.Fatalf("基线不应含远处实体 %d: %v", far, ids)
	}
}

func TestInterestEnteredStayedLeft(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, clipView(2))
	player := createPlayer(t, eng, pid, "u1")
	near := placeProp(t, wa, 1, 0)
	far := placeProp(t, wa, 8, 0)
	wa.viewSnapshot("u1")

	ecs.Get[components.Growable](wa.sim, near).Stage = 2
	ecs.MarkDirty[components.Growable](wa.sim, near)
	ecs.Get[components.Growable](wa.sim, far).Stage = 9
	ecs.MarkDirty[components.Growable](wa.sim, far)
	syncTick(t, eng, pid)

	d := lastDeltaFor(pushed(), "u1")
	if !deltaHasEntity(d, near) {
		t.Fatal("近处 dirty 应进入 stayed 增量")
	}
	if deltaHasEntity(d, far) {
		t.Fatal("远处 dirty 不应下发")
	}

	ecs.Set(wa.sim, player, components.Position{X: 8, Y: 0})
	syncTick(t, eng, pid)
	d = lastDeltaFor(pushed(), "u1")
	if !deltaHasEntity(d, far) {
		t.Fatal("走进远处后应 entered 远处实体")
	}
	if !deltaRemoved(d, near) {
		t.Fatal("走出近处后应 left 近处实体")
	}
}

func TestInterestTwoPlayersSeeDifferentSets(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, clipView(1))
	p1 := createPlayer(t, eng, pid, "a")
	p2 := createPlayer(t, eng, pid, "b")
	ecs.Set(wa.sim, p1, components.Position{X: 0, Y: 0})
	ecs.Set(wa.sim, p2, components.Position{X: 10, Y: 0})
	left := placeProp(t, wa, 0, 0)
	right := placeProp(t, wa, 10, 0)
	syncTick(t, eng, pid)

	da := lastDeltaFor(pushed(), "a")
	db := lastDeltaFor(pushed(), "b")
	if !deltaHasEntity(da, left) || deltaHasEntity(da, right) {
		t.Fatal("玩家 a 应只 entered 左侧")
	}
	if !deltaHasEntity(db, right) || deltaHasEntity(db, left) {
		t.Fatal("玩家 b 应只 entered 右侧")
	}
}

func TestInterestUnlimitedSeesAll(t *testing.T) {
	wa := NewWorldActor(WorldConfig{ViewRadius: -1})
	wa.createPlayer("u1")
	far := placeProp(t, wa, 80, 80)
	snap := wa.viewSnapshot("u1")
	found := false
	for _, st := range snap.Entities {
		if st.EntityId == uint64(far) {
			found = true
		}
	}
	if !found {
		t.Fatal("ViewRadius<0 应不下发裁剪")
	}
}

func TestInterestFiltersFarEvents(t *testing.T) {
	wa := NewWorldActor(clipView(2))
	viewer := wa.createPlayer("u1")
	near := placeProp(t, wa, 1, 0)
	far := placeProp(t, wa, 20, 0)
	visible := wa.collectVisible(viewer)
	events := []*game.WorldEvent{
		{Payload: &game.WorldEvent_HealthChanged{HealthChanged: &game.HealthChangedEvent{
			TargetEntity: uint64(near), Delta: -1,
		}}},
		{Payload: &game.WorldEvent_HealthChanged{HealthChanged: &game.HealthChangedEvent{
			TargetEntity: uint64(far), Delta: -1,
		}}},
		{Payload: &game.WorldEvent_Outcome{Outcome: &game.ActionOutcome{
			EntityId: uint64(viewer),
		}}},
	}
	got := filterViewEvents(events, visible, viewer)
	if len(got) != 2 {
		t.Fatalf("应保留近处血量变化和自己的 outcome，得到 %d", len(got))
	}
}

func bruteVisible(wa *WorldActor, viewer ecs.Entity) map[ecs.Entity]struct{} {
	seen := make(map[ecs.Entity]struct{})
	radius := wa.interestRadius()
	ox, oy, hasOrigin := 0, 0, false
	if ecs.Has[components.Position](wa.sim, viewer) {
		p := ecs.Get[components.Position](wa.sim, viewer)
		ox, oy, hasOrigin = p.X, p.Y, true
	}
	ecs.Query[components.Position](wa.sim, func(e ecs.Entity, p *components.Position) {
		if !hasOrigin || inViewSquare(p.X, p.Y, ox, oy, radius) {
			seen[e] = struct{}{}
		}
	})
	return seen
}

func TestCollectVisibleMatchesScan(t *testing.T) {
	wa := NewWorldActor(clipView(3))
	viewer := wa.createPlayer("u1")
	p := ecs.Get[components.Position](wa.sim, viewer)
	placeProp(t, wa, p.X+3, p.Y)
	placeProp(t, wa, p.X+4, p.Y)
	placeProp(t, wa, p.X-2, p.Y+2)
	placeProp(t, wa, p.X+100, p.Y+100)
	got := wa.collectVisible(viewer)
	want := bruteVisible(wa, viewer)
	for e := range want {
		if _, ok := got[e]; !ok {
			t.Fatalf("可见集漏了实体 %d", e)
		}
	}
	for e := range got {
		if _, ok := want[e]; !ok {
			if e == viewer {
				continue
			}
			if ecs.Has[components.Position](wa.sim, e) {
				t.Fatalf("可见集多了实体 %d", e)
			}
		}
	}
}

func TestInterestPreloadSendsBeyondCamera(t *testing.T) {
	wa := NewWorldActor(WorldConfig{ViewRadius: 2, ViewPreload: 4})
	wa.createPlayer("u1")
	ring := placeProp(t, wa, 5, 0) // 相机外、预加载内（2+4=6）
	beyond := placeProp(t, wa, 8, 0)
	snap := wa.viewSnapshot("u1")
	ids := map[uint64]bool{}
	for _, st := range snap.Entities {
		ids[st.EntityId] = true
	}
	if !ids[uint64(ring)] {
		t.Fatal("预加载圈内实体应已下发，供屏幕外缓冲")
	}
	if ids[uint64(beyond)] {
		t.Fatal("超过 view_radius+view_preload 的实体不应下发")
	}
	pc := wa.config.ToProto()
	if pc.ViewRadius != 2 {
		t.Fatalf("契约 view_radius 应仍是相机下限 2，得到 %d", pc.ViewRadius)
	}
	if pc.ViewRadiusMax != 2 {
		t.Fatalf("未设 view_radius_max 时应等于下限 2，得到 %d", pc.ViewRadiusMax)
	}
	if pc.ViewPreload != 4 {
		t.Fatalf("契约 view_preload = %d, want 4", pc.ViewPreload)
	}
}

func TestInterestUsesRadiusMaxNotCameraMin(t *testing.T) {
	wa := NewWorldActor(WorldConfig{ViewRadius: 2, ViewRadiusMax: 6, ViewPreload: 4})
	wa.createPlayer("u1")
	inMax := placeProp(t, wa, 5, 0)     // 下限外、上限内
	inPreload := placeProp(t, wa, 9, 0) // 上限外、预加载内（6+4=10）
	beyond := placeProp(t, wa, 11, 0)
	snap := wa.viewSnapshot("u1")
	ids := map[uint64]bool{}
	for _, st := range snap.Entities {
		ids[st.EntityId] = true
	}
	if !ids[uint64(inMax)] {
		t.Fatal("拉远上限内的实体应已下发")
	}
	if !ids[uint64(inPreload)] {
		t.Fatal("相对上限的预加载圈内实体应已下发")
	}
	if ids[uint64(beyond)] {
		t.Fatal("超过 view_radius_max+view_preload 的实体不应下发")
	}
	pc := wa.config.ToProto()
	if pc.ViewRadius != 2 || pc.ViewRadiusMax != 6 {
		t.Fatalf("契约范围 = %d..%d, want 2..6", pc.ViewRadius, pc.ViewRadiusMax)
	}
}

func TestViewSnapshotSeedsInterestCursor(t *testing.T) {
	eng, pid, wa, pushed := newM5World(t, clipView(2))
	createPlayer(t, eng, pid, "u1")
	near := placeProp(t, wa, 1, 0)
	wa.viewSnapshot("u1")
	wa.sim.DrainDirtySorted()
	syncTick(t, eng, pid)
	d := lastDeltaFor(pushed(), "u1")
	if deltaHasEntity(d, near) {
		t.Fatal("登录基线已记下兴趣集后，静止实体不应再 entered")
	}
}
