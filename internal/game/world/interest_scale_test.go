package world

import (
	"fmt"
	"testing"
	"time"

	pb "google.golang.org/protobuf/proto"

	"github.com/TheChosenGay/actor"
	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

type interestScaleCase struct {
	name       string
	entities   int
	players    int
	viewRadius int
}

type stubTickCtx struct{}

func (stubTickCtx) Message() any         { return Tick{} }
func (stubTickCtx) Sender() *actor.PID   { return nil }
func (stubTickCtx) PID() *actor.PID      { return &actor.PID{ID: "world/scale"} }
func (stubTickCtx) Send(*actor.PID, any) {}
func (stubTickCtx) Request(*actor.PID, any, time.Duration) *actor.Request {
	return nil
}
func (stubTickCtx) Respond(any)                                  {}
func (stubTickCtx) SpawnChild(actor.Producer, string) *actor.PID { return nil }
func (stubTickCtx) SendRepeat(*actor.PID, any, time.Duration) actor.ISendRepeater {
	return nil
}

func seedInterestWorld(t testing.TB, tc interestScaleCase) *WorldActor {
	t.Helper()
	wa := NewWorldActor(WorldConfig{ViewRadius: tc.viewRadius, HungerRate: 0})
	for i := 0; i < tc.players; i++ {
		p := wa.createPlayer(fmt.Sprintf("u%d", i))
		ecs.Set(wa.sim, p, components.Position{X: (i * 17) % 64, Y: (i * 13) % 64})
	}
	for i := 0; i < tc.entities; i++ {
		e := wa.sim.CreateEntity()
		ecs.Add(wa.sim, e, components.Position{X: (i * 31) % 128, Y: (i * 47) % 128})
	}
	return wa
}

func warmupInterest(wa *WorldActor) {
	wa.sim.DrainDirtySorted()
	for _, viewer := range wa.onlineViewers() {
		wa.viewSnapshot(wa.players[viewer])
	}
	for i := 0; i < 3; i++ {
		wa.onTick(stubTickCtx{})
	}
}

func lastVisibleCount(wa *WorldActor) int {
	for _, viewer := range wa.onlineViewers() {
		return len(wa.interest[viewer])
	}
	return 0
}

func TestInterestScaleImpact(t *testing.T) {
	cases := []interestScaleCase{
		{name: "200e_1p_r16", entities: 200, players: 1, viewRadius: 16},
		{name: "1000e_1p_r24", entities: 1000, players: 1, viewRadius: 24},
		{name: "1000e_4p_r24", entities: 1000, players: 4, viewRadius: 24},
		{name: "5000e_4p_r24", entities: 5000, players: 4, viewRadius: 24},
		{name: "5000e_8p_r16", entities: 5000, players: 8, viewRadius: 16},
		{name: "5000e_4p_unlimited", entities: 5000, players: 4, viewRadius: -1},
	}
	const samples = 20
	t.Logf("%-22s %8s %8s %8s %8s %10s %10s %8s",
		"case", "tick_µs", "proj_µs", "scan_µs", "enc_µs", "view_B", "bcast_B", "visible")
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			wa := seedInterestWorld(t, tc)
			warmupInterest(wa)

			var last TickStats
			wa.SetTickObserver(TickObserverFunc(func(stats TickStats) { last = stats }))

			var tick, proj, scan, enc time.Duration
			var viewBytes int
			for i := 0; i < samples; i++ {
				wa.onTick(stubTickCtx{})
				tick += last.Duration
				proj += last.ProjectionDuration
				scan += last.ViewScanDuration
				enc += last.ViewEncodeDuration
				viewBytes += last.DeltaSnapshotBytes
			}
			n := time.Duration(samples)
			vis := lastVisibleCount(wa)
			bcast := pb.Size(FullSnapshot(wa.sim)) * max(1, tc.players)

			t.Logf("%-22s %8.1f %8.1f %8.1f %8.1f %10d %10d %8d",
				tc.name,
				float64(tick/n)/1e3,
				float64(proj/n)/1e3,
				float64(scan/n)/1e3,
				float64(enc/n)/1e3,
				viewBytes/samples,
				bcast,
				vis,
			)
			if last.ProjectionDuration <= 0 || last.Duration < last.ProjectionDuration {
				t.Fatalf("tick=%v proj=%v", last.Duration, last.ProjectionDuration)
			}
			if tc.viewRadius > 0 && viewBytes/samples > bcast {
				t.Fatalf("稳态视野下发 %d 不应大于全图快照×人数 %d", viewBytes/samples, bcast)
			}
		})
	}
}

func TestInterestScaleColdStartVsSteady(t *testing.T) {
	wa := seedInterestWorld(t, interestScaleCase{entities: 5000, players: 4, viewRadius: 24})
	var cold TickStats
	wa.SetTickObserver(TickObserverFunc(func(stats TickStats) { cold = stats }))
	wa.onTick(stubTickCtx{})
	wa.SetTickObserver(nil)
	warmupInterest(wa)
	var steady TickStats
	wa.SetTickObserver(TickObserverFunc(func(stats TickStats) { steady = stats }))
	wa.onTick(stubTickCtx{})
	t.Logf("cold tick=%.1fµs proj=%.1fµs bytes=%d dirty=%d",
		float64(cold.Duration)/1e3, float64(cold.ProjectionDuration)/1e3, cold.DeltaSnapshotBytes, cold.DirtyEntities)
	t.Logf("steady tick=%.1fµs proj=%.1fµs scan=%.1fµs enc=%.1fµs bytes=%d dirty=%d visible=%d",
		float64(steady.Duration)/1e3, float64(steady.ProjectionDuration)/1e3,
		float64(steady.ViewScanDuration)/1e3, float64(steady.ViewEncodeDuration)/1e3,
		steady.DeltaSnapshotBytes, steady.DirtyEntities, lastVisibleCount(wa))
	if cold.DeltaSnapshotBytes <= steady.DeltaSnapshotBytes {
		t.Fatal("冷启动应把创建期 dirty 打进 entered/stayed，体积应明显大于稳态")
	}
}

func TestBroadcastVsViewBytes(t *testing.T) {
	wa := seedInterestWorld(t, interestScaleCase{entities: 800, players: 2, viewRadius: 12})
	warmupInterest(wa)
	var last TickStats
	wa.SetTickObserver(TickObserverFunc(func(stats TickStats) { last = stats }))
	wa.onTick(stubTickCtx{})
	full := pb.Size(FullSnapshot(wa.sim))
	t.Logf("login_full=%d visible=%d steady_view_bytes=%d steady_tick=%.1fµs proj=%.1fµs",
		full, lastVisibleCount(wa), last.DeltaSnapshotBytes,
		float64(last.Duration)/1e3, float64(last.ProjectionDuration)/1e3)
	if last.DeltaSnapshotBytes <= 0 {
		t.Fatal("应能测到编码体积")
	}
}

func BenchmarkInterestProjection(b *testing.B) {
	cases := []interestScaleCase{
		{name: "1000e_1p", entities: 1000, players: 1, viewRadius: 24},
		{name: "5000e_4p", entities: 5000, players: 4, viewRadius: 24},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			wa := seedInterestWorld(b, tc)
			warmupInterest(wa)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				wa.onTick(stubTickCtx{})
			}
		})
	}
}

func fullGameplayCfg(viewRadius int) WorldConfig {
	cfg := realWorldCfg()
	cfg.HungerRate = 1
	cfg.BuildingsPath = "../../../configs/buildings.json"
	cfg.ViewRadius = viewRadius
	return cfg
}

func countPositions(wa *WorldActor) int {
	n := 0
	ecs.Query[components.Position](wa.sim, func(_ ecs.Entity, _ *components.Position) { n++ })
	return n
}

func TestInterestRealGameplayScale(t *testing.T) {
	cases := []struct {
		name       string
		players    int
		viewRadius int
	}{
		{name: "real_1p_r24", players: 1, viewRadius: 24},
		{name: "real_2p_r24", players: 2, viewRadius: 24},
		{name: "real_4p_r24", players: 4, viewRadius: 24},
		{name: "real_8p_r24", players: 8, viewRadius: 24},
		{name: "real_4p_unlimited", players: 4, viewRadius: -1},
	}
	const warmupTicks, samples = 30, 50
	t.Logf("%-20s %6s %8s %8s %8s %8s %8s %8s %8s %8s",
		"case", "ents", "tick_µs", "sim_µs", "proj_µs", "scan_µs", "enc_µs", "view_B", "dirty", "visible")
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			wa := NewWorldActor(fullGameplayCfg(tc.viewRadius))
			for i := 0; i < tc.players; i++ {
				p := wa.createPlayer(fmt.Sprintf("p%d", i))
				ecs.Set(wa.sim, p, components.Position{
					X: 64 + (i%4)*16,
					Y: 64 + (i/4)*16,
				})
			}
			ents := countPositions(wa)
			warmupInterest(wa)
			for i := 0; i < warmupTicks; i++ {
				wa.onTick(stubTickCtx{})
			}

			var last TickStats
			wa.SetTickObserver(TickObserverFunc(func(stats TickStats) { last = stats }))
			var tick, proj, scan, enc time.Duration
			var bytes, dirty int
			for i := 0; i < samples; i++ {
				wa.onTick(stubTickCtx{})
				tick += last.Duration
				proj += last.ProjectionDuration
				scan += last.ViewScanDuration
				enc += last.ViewEncodeDuration
				bytes += last.DeltaSnapshotBytes
				dirty += last.DirtyEntities
			}
			n := time.Duration(samples)
			sim := tick - proj
			t.Logf("%-20s %6d %8.1f %8.1f %8.1f %8.1f %8.1f %8d %8.1f %8d",
				tc.name, ents,
				float64(tick/n)/1e3,
				float64(sim/n)/1e3,
				float64(proj/n)/1e3,
				float64(scan/n)/1e3,
				float64(enc/n)/1e3,
				bytes/samples,
				float64(dirty)/samples,
				lastVisibleCount(wa),
			)
			if last.Duration <= 0 || last.ProjectionDuration <= 0 {
				t.Fatal("应测到整 tick 与投影")
			}
		})
	}
}

func expandMapData(wa *WorldActor, w, h int) {
	md, ok := ecs.TryResource[MapData](wa.sim)
	if !ok {
		md = wa.attachMap(&MapData{Width: w, Height: h})
	}
	md.Width, md.Height = w, h
	md.CornerHeights = make([]byte, (w+1)*(h+1))
	md.CornerTypes = make([]byte, (w+1)*(h+1))
	md.TileEffects = make([]byte, w*h)
	md.TileParams = make([]int8, w*h)
	md.RegionIDs = make([]byte, w*h)
	md.Occupied = make([]uint16, w*h)
}

func seedLargeGameplayWorld(t testing.TB, mapW, mapH, trees, wolves, rabbits, players, viewRadius int) *WorldActor {
	t.Helper()
	cfg := fullGameplayCfg(viewRadius)
	cfg.WeatherFrameTicks = -1 // 天气相位仍推进；关掉 1Hz 雾网格推送，避免大地图采样盖住 tick
	wa := NewWorldActor(cfg)
	expandMapData(wa, mapW, mapH)
	for i := 0; i < trees; i++ {
		e := wa.sim.CreateEntity()
		ecs.Add(wa.sim, e, components.Position{X: (i * 31) % mapW, Y: (i * 47) % mapH})
		ecs.Add(wa.sim, e, interactive.Choppable{Kind: components.ItemWood, WorkLeft: 4, MaxWork: 4})
	}
	for i := 0; i < wolves; i++ {
		spawnAICreature(t, wa, (i*541)%mapW, (i*1049)%mapH, true)
	}
	for i := 0; i < rabbits; i++ {
		spawnAICreature(t, wa, (i*811)%mapW, (i*1237)%mapH, false)
	}
	for i := 0; i < players; i++ {
		p := wa.createPlayer(fmt.Sprintf("p%d", i))
		ecs.Set(wa.sim, p, components.Position{
			X: (mapW/8 + i*(mapW/7)) % mapW,
			Y: (mapH/8 + i*(mapH/5)) % mapH,
		})
	}
	return wa
}

func TestInterestLargeGameplayScale(t *testing.T) {
	cases := []struct {
		name                   string
		mapW, mapH             int
		trees, wolves, rabbits int
		players, viewRadius    int
	}{
		{name: "512_2k_6p", mapW: 512, mapH: 512, trees: 2000, wolves: 40, rabbits: 80, players: 6, viewRadius: 24},
		{name: "512_5k_6p", mapW: 512, mapH: 512, trees: 5000, wolves: 60, rabbits: 120, players: 6, viewRadius: 24},
		{name: "1024_10k_6p", mapW: 1024, mapH: 1024, trees: 10000, wolves: 80, rabbits: 160, players: 6, viewRadius: 24},
		{name: "1024_20k_6p", mapW: 1024, mapH: 1024, trees: 20000, wolves: 100, rabbits: 200, players: 6, viewRadius: 24},
		{name: "1024_10k_6p_uncut", mapW: 1024, mapH: 1024, trees: 10000, wolves: 80, rabbits: 160, players: 6, viewRadius: -1},
	}
	const warmupTicks, samples = 20, 30
	t.Logf("%-20s %6s %8s %8s %8s %8s %8s %8s %8s %8s",
		"case", "ents", "tick_µs", "sim_µs", "proj_µs", "scan_µs", "enc_µs", "view_B", "dirty", "visible")
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			wa := seedLargeGameplayWorld(t, tc.mapW, tc.mapH, tc.trees, tc.wolves, tc.rabbits, tc.players, tc.viewRadius)
			ents := countPositions(wa)
			warmupInterest(wa)
			for i := 0; i < warmupTicks; i++ {
				wa.onTick(stubTickCtx{})
			}
			var last TickStats
			wa.SetTickObserver(TickObserverFunc(func(stats TickStats) { last = stats }))
			var tick, proj, scan, enc time.Duration
			var bytes, dirty int
			for i := 0; i < samples; i++ {
				wa.onTick(stubTickCtx{})
				tick += last.Duration
				proj += last.ProjectionDuration
				scan += last.ViewScanDuration
				enc += last.ViewEncodeDuration
				bytes += last.DeltaSnapshotBytes
				dirty += last.DirtyEntities
			}
			n := time.Duration(samples)
			t.Logf("%-20s %6d %8.1f %8.1f %8.1f %8.1f %8.1f %8d %8.1f %8d",
				tc.name, ents,
				float64(tick/n)/1e3,
				float64((tick-proj)/n)/1e3,
				float64(proj/n)/1e3,
				float64(scan/n)/1e3,
				float64(enc/n)/1e3,
				bytes/samples,
				float64(dirty)/samples,
				lastVisibleCount(wa),
			)
			if last.Duration <= 0 || last.ProjectionDuration <= 0 {
				t.Fatal("应测到整 tick 与投影")
			}
		})
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
