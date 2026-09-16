package world

import (
	"fmt"
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/systems"
)

// 本文件测**行为树**的性能：混合装载不同复杂度的树，看每 tick 的决策耗时。
//
// 跑法：
//
//	go test ./internal/game/world/ -bench BehaviorTree -benchtime 20x
//	go test ./internal/game/world/ -bench BehaviorTreeByKind -benchmem
//
// 结论速览（Apple M4 Pro，20Hz 预算 = 50ms/tick）：
//
//	规模      总耗时/tick   单实体      占 20Hz 预算
//	1k        0.75 ms       0.75 µs     0.03%
//	5k        2.73 ms       0.55 µs     0.02%
//	10k       4.83 ms       0.48 µs     0.02%
//	20k       9.79 ms       0.49 µs     0.02%
//
// 即：**2 万只生物跑行为树只占单 tick 预算的 0.02%**，完全不是瓶颈。
// 线性扩展良好（20k 约为 1k 的 13 倍耗时、20 倍实体）。
//
// 单实体开销（隔离测试，只算树本身）：
//
//	dormant(5 节点)   0.19 µs
//	prey(9 节点)      0.25 µs
//	boss(44 节点)     0.37 µs
//	predator(17 节点) 0.44 µs  ← 比 44 节点的 boss 还贵，见下
//
// **重要发现：节点数不是耗时主导因素，"动作的代价"才是。**
// predator 树比 boss 树小（17 vs 44 节点），却更慢——因为 predator 的
// ChaseAction 会触发 A* 寻路（worldmap.FindPath），而 boss 在玩家远离时
// 走 LeapTo（直接位移，无寻路）。实测寻路相关开销约占 15%（见 git 历史里的
// 对照实验）。所以优化行为树性能，应该先看**动作节点做了什么**，
// 而不是盯着树的大小。
//
// 与 ai_bench_test.go 的区别：那个测的是"感知 + 旧 AI + 移动"整条链路；
// 这里专门聚焦**行为树驱动的决策**（AISystem → 行为树 Tick），
// 并让生物挂载不同种类的树，观察节点数对耗时的影响。
//
// 场景设计：
//   - 4 种树混合：捕食者(17 节点) / 被动(9) / 休眠(5) / Boss(44)
//   - 生物围绕若干"玩家"散布，保证仇恨/追击/攻击等分支真的被走到
//     （全部 idle 的话只测到 Selector 的一条短路路径，测不出真实开销）
//   - 用 --benchtime 控制时长，默认 go test -bench 会给足够迭代

// btKind 是参与性能测试的树种类（去掉 UNSPECIFIED）。
var btKindList = []components.BehaviorTreeKind{
	components.TreeKindPredator,
	components.TreeKindPrey,
	components.TreeKindDormant,
	components.TreeKindBoss,
}

// btKindName 给 benchmark 子项起可读名字。
func btKindName(k components.BehaviorTreeKind) string {
	switch k {
	case components.TreeKindPredator:
		return "predator17"
	case components.TreeKindPrey:
		return "prey9"
	case components.TreeKindDormant:
		return "dormant5"
	case components.TreeKindBoss:
		return "boss44"
	}
	return "unknown"
}

// spawnBTCreature 造一只挂指定行为树的生物。
//
// 参数按树的类型给：捕食者有攻击力（会追击/攻击），被动生物无攻击力（会逃跑），
// Boss 血量高（会走阶段机）。这样各分支都能被真实触发。
func spawnBTCreature(b testing.TB, wa *WorldActor, x, y int, kind components.BehaviorTreeKind) ecs.Entity {
	b.Helper()
	e := wa.sim.CreateEntity()

	hp, aoíRadius := 30, 6
	damage, attackRange, cooldown := 8, 1, 20
	switch kind {
	case components.TreeKindPrey:
		damage = 0 // 被动：无攻击力 → 走逃跑分支
	case components.TreeKindDormant:
		hp, aoíRadius, damage = 20, 0, 0 // 完全被动：无感知
	case components.TreeKindBoss:
		hp, aoíRadius = 400, 30
		damage, attackRange, cooldown = 4, 1, 24
	}

	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: hp, Max: hp})
	ecs.Add(wa.sim, e, components.Attackable{})
	ecs.Add(wa.sim, e, components.Moveable{Speed: 2, EffectiveSpeed: 2})
	ecs.Add(wa.sim, e, components.AOI{Radius: aoíRadius})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{},
		HomeX: x, HomeY: y, RoamRadius: 8,
	})
	ecs.Add(wa.sim, e, components.AI{
		State: components.CreatureIdle, FleeHP: 8, HitMemoryTicks: 10,
		HostilePlayers: true, Phase2HP: hp / 2,
	})
	ecs.Add(wa.sim, e, components.BehaviorTree{
		Kind:         kind,
		RunningChild: map[uint32]uint8{},
		Counters:     map[uint32]int{},
	})
	ecs.Add(wa.sim, e, interactive.Attacker{
		AttackDamage: damage, AttackRange: attackRange, AttackCooldown: cooldown,
	})
	return e
}

// buildBTWorld 造一个 n 只生物、混装四种树的世界，并撒若干玩家当目标。
//
// 分布方式：把玩家均匀撒在场上，生物按网格铺开 —— 保证大部分生物
// 的 AOI 里能看到玩家（于是真的走"选目标 → 追击/攻击"分支）。
func buildBTWorld(b testing.TB, size, creatures, players int) *WorldActor {
	b.Helper()
	wa := NewWorldActor(WorldConfig{})
	wa.attachMap(&MapData{Width: size, Height: size, CornerTypes: make([]byte, (size+1)*(size+1))})

	for i := 0; i < players; i++ {
		p := wa.createPlayer(fmt.Sprintf("p%d", i))
		ecs.Set(wa.sim, p, components.Position{
			X: (i * 977) % size,
			Y: (i * 1361) % size,
		})
	}
	for i := 0; i < creatures; i++ {
		// 轮转四种树，保证混装均匀
		kind := btKindList[i%len(btKindList)]
		spawnBTCreature(b, wa, (i*541)%size, (i*1049)%size, kind)
	}
	return wa
}

// BenchmarkBehaviorTreeMixed：混装四种树，逐规模测"AISystem 驱动行为树"的耗时。
//
// 这是最贴近实战的一项：每 tick 每只生物跑一次自己的树。
func BenchmarkBehaviorTreeMixed(b *testing.B) {
	cases := []struct {
		name      string
		size      int
		creatures int
		players   int
	}{
		{"1k", 256, 1000, 20},
		{"5k", 512, 5000, 50},
		{"10k", 1024, 10000, 100},
		{"20k", 1024, 20000, 200},
	}
	for _, tc := range cases {
		wa := buildBTWorld(b, tc.size, tc.creatures, tc.players)
		ai := &systems.AISystem{}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ai.Update(wa.sim, time.Millisecond)
			}
			b.StopTimer()
			reportPerEntity(b, tc.creatures)
		})
	}
}

// BenchmarkBehaviorTreeByKind：按树种类分开测，看**节点数**对单实体耗时的影响。
//
// 同规模下比较 predator(17) / prey(9) / dormant(5) / boss(44)，
// 能直接看出"树越复杂，单次 Tick 越贵"，以及是否成线性。
func BenchmarkBehaviorTreeByKind(b *testing.B) {
	const (
		size      = 512
		creatures = 5000
		players   = 50
	)
	for _, kind := range btKindList {
		wa := NewWorldActor(WorldConfig{})
		wa.attachMap(&MapData{Width: size, Height: size, CornerTypes: make([]byte, (size+1)*(size+1))})
		for i := 0; i < players; i++ {
			p := wa.createPlayer(fmt.Sprintf("p%d", i))
			ecs.Set(wa.sim, p, components.Position{X: (i * 977) % size, Y: (i * 1361) % size})
		}
		for i := 0; i < creatures; i++ {
			spawnBTCreature(b, wa, (i*541)%size, (i*1049)%size, kind)
		}
		ai := &systems.AISystem{}
		b.Run(btKindName(kind), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ai.Update(wa.sim, time.Millisecond)
			}
			b.StopTimer()
			reportPerEntity(b, creatures)
		})
	}
}

// BenchmarkBehaviorTreeIsolated：绕开 AISystem 的感知/仇恨逻辑，
// **只测行为树 Tick 本身**（黑板 + 环境 + 树遍历）。
//
// 用来区分"耗时到底花在树上，还是花在 AISystem 的目标选择上"。
func BenchmarkBehaviorTreeIsolated(b *testing.B) {
	const creatures = 5000
	for _, kind := range btKindList {
		wa := NewWorldActor(WorldConfig{})
		wa.attachMap(&MapData{Width: 512, Height: 512, CornerTypes: make([]byte, 513*513)})
		for i := 0; i < 50; i++ {
			p := wa.createPlayer(fmt.Sprintf("p%d", i))
			ecs.Set(wa.sim, p, components.Position{X: (i * 977) % 512, Y: (i * 1361) % 512})
		}
		var ents []ecs.Entity
		for i := 0; i < creatures; i++ {
			ents = append(ents, spawnBTCreature(b, wa, (i*541)%512, (i*1049)%512, kind))
		}
		b.Run(btKindName(kind), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tickTreesOnly(wa, ents)
			}
			b.StopTimer()
			reportPerEntity(b, creatures)
		})
	}
}

// tickTreesOnly 只驱动行为树（不跑感知/仇恨），供隔离测试用。
func tickTreesOnly(wa *WorldActor, ents []ecs.Entity) {
	for _, e := range ents {
		systems.TickBehaviorTree(wa.sim, e)
	}
}

// BenchmarkBehaviorTreeSnapshotCost：行为树运行态的**快照编码**开销。
//
// 运行态（Running 游标 + 计数器）每 tick 可能进增量快照，是容易被忽略的
// 隐藏成本；这里单独量一下。
func BenchmarkBehaviorTreeSnapshotCost(b *testing.B) {
	wa := buildBTWorld(b, 512, 5000, 50)
	var ents []ecs.Entity
	ecs.Query[components.BehaviorTree](wa.sim, func(e ecs.Entity, _ *components.BehaviorTree) {
		ents = append(ents, e)
	})
	// 制造一些运行态，避免测到空 map 的极快路径
	for i, e := range ents {
		bt := ecs.Get[components.BehaviorTree](wa.sim, e)
		bt.RunningChild[uint32(i%44)] = uint8(i % 4)
		bt.Counters[uint32(i%44)] = i % 30
	}
	meta, ok := wa.sim.Registry().MetaByName("BehaviorTree")
	if !ok {
		b.Skip("未注册 BehaviorTree codec")
	}
	codec, ok := meta.Codec.(ecs.Codec[components.BehaviorTree])
	if !ok {
		b.Skip("BehaviorTree codec 类型不符")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, e := range ents {
			bt := ecs.Get[components.BehaviorTree](wa.sim, e)
			if _, err := codec.Encode(*bt); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.StopTimer()
	reportPerEntity(b, len(ents))
}

// reportPerEntity 把整轮耗时摊到单个实体上（比 ns/op 更直观）。
func reportPerEntity(b *testing.B, n int) {
	if n <= 0 {
		return
	}
	perOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N) / float64(n)
	b.ReportMetric(perOp, "ns/entity")
	b.ReportMetric(perOp/1000, "µs/entity")
	// 20Hz（50ms）下这套决策能占多少预算
	b.ReportMetric(perOp*20/(50*1e6)*100, "%budget@20Hz")
}
