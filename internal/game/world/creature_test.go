package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
	"starve/internal/game/worldmap"
	game "starve/pkg/proto/game"
)

// addWolf 摆一只狼（主动：仇恨/追击/攻击）。
func addWolf(t *testing.T, wa *WorldActor, x, y int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: 30, Max: 30})
	ecs.Add(wa.sim, e, components.Attackable{})
	ecs.Add(wa.sim, e, components.Moveable{Speed: intervalToSpeed(1, 0.05)})
	ecs.Add(wa.sim, e, components.AOI{Radius: 6})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{}, HomeX: x, HomeY: y, RoamRadius: 0,
		Drops: []components.ItemStack{{Kind: components.ItemMeat, Count: 2}},
	})
	ecs.Add(wa.sim, e, components.DropSource{Category: components.DropSourceCreature, CreatureKind: components.CreatureWolf})
	template := wa.config.Creatures[components.CreatureWolf]
	template.Drops = []components.DropRule{{
		Kind: components.ItemMeat, MinCount: 2, MaxCount: 2, Chance: components.DropChanceScale,
	}}
	wa.config.Creatures[components.CreatureWolf] = template
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle, HitMemoryTicks: 5, HostilePlayers: true})
	addBehaviorTree(wa, e, true) // 掠食者树
	ecs.Add(wa.sim, e, interactive.Attacker{AttackRange: 1, AttackDamage: 8, AttackCooldown: 5})
	return e
}

// addBehaviorTree 给测试生物挂行为树。
//
// 为什么测试必须显式挂：AISystem 在没有 BehaviorTree 组件时会回退到
// legacy 状态机（为了兼容旧存档）。如果不挂，测试测的就是回退路径，
// 行为树的 bug 会被"测试全绿"掩盖——这正是本 helper 存在的意义。
func addBehaviorTree(wa *WorldActor, e ecs.Entity, canAttack bool) {
	kind := components.TreeKindForTemplate(canAttack)
	ecs.Add(wa.sim, e, components.BehaviorTree{
		Kind:         kind,
		RunningChild: map[uint32]uint8{},
		Counters:     map[uint32]int{},
	})
}

// 仇恨 + 追击 + 攻击：玩家进入感知半径 → 狼锁定并攻击，玩家掉血。
func TestCreatureAggroChaseAttack(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wolf := addWolf(t, wa, 0, 0)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	hp := ecs.Get[components.Health](wa.sim, player)

	tickWorld(wa) // 感知 → 目标 → 接纳攻击（同格，范围 1）
	ai := ecs.Get[components.AI](wa.sim, wolf)
	if ai.Target != player || ai.State != components.CreatureAttack {
		t.Fatalf("狼应锁定玩家并攻击: target=%d state=%v", ai.Target, ai.State)
	}
	runActionTicks(wa, 8)
	if hp.Cur != 92 {
		t.Fatalf("狼攻击应扣 8 血: hp=%d want 92", hp.Cur)
	}
	// recovery 完成后再起一轮并在 commit tick 命中
	for i := 0; i < 17; i++ {
		tickWorld(wa)
	}
	if hp.Cur >= 92 {
		t.Fatalf("冷却后应继续攻击: hp=%d", hp.Cur)
	}
}

// 玩家攻击生物 → 仇恨表记录攻击者 → 生物切换为追击。
func TestCreaturePlayerAttackThreat(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wolf := addWolf(t, wa, 0, 0)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: wolf}})
	runActionTicks(wa, 9)
	c := ecs.Get[components.Creature](wa.sim, wolf)
	ai := ecs.Get[components.AI](wa.sim, wolf)
	if c.Threats[player] == 0 {
		t.Fatalf("攻击后应有仇恨: %+v", c.Threats)
	}
	if ai.LastHitBy != player {
		t.Fatalf("攻击后应记录受击标记: last_hit_by=%d", ai.LastHitBy)
	}
	tickWorld(wa)
	ai = ecs.Get[components.AI](wa.sim, wolf)
	if ai.Target != player || ai.State != components.CreatureAttack {
		t.Fatalf("受击后狼应追击玩家: target=%d state=%v", ai.Target, ai.State)
	}
}

// 死亡掉落：狼死亡后保留 Dead 尸体，另建 Loot（移除 Creature）→ 拾取进背包。
func TestCreatureDeathDrops(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	wolf := addWolf(t, wa, 0, 0)
	ecs.Get[components.AI](wa.sim, wolf).Cooldown = 1000 // 本测试只验证玩家击杀与掉落
	syncWorld(t, eng, pid)

	// 玩家攻击 3 次（每个动作含 windup/recovery）→ 狼 30 血归零
	for i := 0; i < 3; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: wolf}})
		ticks := 17
		if i == 2 {
			ticks = 9
		}
		for j := 0; j < ticks; j++ {
			eng.Send(pid, Tick{})
		}
	}
	syncWorld(t, eng, pid)

	if !ecs.Has[components.Dead](wa.sim, wolf) {
		t.Fatal("狼应死亡")
	}
	if ecs.Has[components.Creature](wa.sim, wolf) {
		t.Fatal("死亡后应移除 Creature 组件")
	}
	if ecs.Has[components.Lootable](wa.sim, wolf) {
		t.Fatal("尸体不应携带 Lootable")
	}
	lootEntity := findLootableKind(t, wa, components.ItemMeat)
	if lootEntity == wolf {
		t.Fatal("掉落物必须是独立实体")
	}

	// 拾取 → 背包肉 +2
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: lootEntity}})
	eng.Send(pid, Tick{})
	syncWorld(t, eng, pid)
	inv := ecs.Get[components.Inventory](wa.sim, player)
	if inv.CountOf(components.ItemMeat) != 2 {
		t.Fatalf("拾取后肉 = %d, want 2", inv.CountOf(components.ItemMeat))
	}
}

// 寻路：BFS 绕开水体，确定性；不可达返回空。
func TestFindPath(t *testing.T) {
	const grass = byte(game.TerrainType_TERRAIN_TYPE_GRASS)
	const water = byte(game.TerrainType_TERRAIN_TYPE_WATER)
	md := &worldmap.MapData{Width: 5, Height: 5, CornerTypes: make([]byte, 6*6)}
	for i := range md.CornerTypes {
		md.CornerTypes[i] = grass
	}
	// 中间 (2,2) 是水，绕行
	md.CornerTypes[2*6+2] = water

	path := worldmap.FindPath(md, 0, 0, 4, 4)
	if len(path) == 0 {
		t.Fatal("应有绕行路径")
	}
	x, y := 0, 0
	for _, d := range path {
		x += d.DX
		y += d.DY
		if x == 2 && y == 2 {
			t.Fatal("路径不应踩水")
		}
	}
	if x != 4 || y != 4 {
		t.Fatalf("路径终点 = (%d,%d), want (4,4)", x, y)
	}
	again := worldmap.FindPath(md, 0, 0, 4, 4)
	if len(again) != len(path) {
		t.Fatal("寻路应确定性一致")
	}

	// 目标被水包围 → 不可达
	md2 := &worldmap.MapData{Width: 3, Height: 3, CornerTypes: make([]byte, 4*4)}
	for i := range md2.CornerTypes {
		md2.CornerTypes[i] = water
	}
	md2.CornerTypes[0] = grass // 起点 (0,0) 可走
	if p := worldmap.FindPath(md2, 0, 0, 2, 2); len(p) != 0 {
		t.Fatalf("不可达应返回空路径, got %v", p)
	}
}

// 游荡：被动生物在出生点附近活动，不超出半径。
func TestCreatureRoam(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 10, Y: 10})
	ecs.Add(wa.sim, e, components.Health{Cur: 10, Max: 10})
	ecs.Add(wa.sim, e, components.Attackable{})
	ecs.Add(wa.sim, e, components.Moveable{Speed: intervalToSpeed(1, 0.05)})
	ecs.Add(wa.sim, e, components.AOI{Radius: 0})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureRabbit, Threats: map[ecs.Entity]int32{}, HomeX: 10, HomeY: 10, RoamRadius: 6,
	})
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle, HitMemoryTicks: 5})
	addBehaviorTree(wa, e, false)
	ecs.Add(wa.sim, e, interactive.Attacker{AttackRange: 1})

	moved := false
	for i := 0; i < 100; i++ {
		tickWorld(wa)
		p := ecs.Get[components.Position](wa.sim, e)
		if p.X != 10 || p.Y != 10 {
			moved = true
		}
		if p.Manhattan(components.Position{X: 10, Y: 10}) > 6 {
			t.Fatalf("游荡超出半径: (%d,%d)", p.X, p.Y)
		}
	}
	if !moved {
		t.Fatal("被动生物应会游荡移动")
	}
}

// 逃跑：血量低于 FleeHP → 切 flee 并远离威胁目标。
func TestCreatureFlee(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AOIInterval: 1})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 4, Y: 5})

	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 5, Y: 5})
	ecs.Add(wa.sim, e, components.Health{Cur: 20, Max: 20})
	ecs.Add(wa.sim, e, components.Attackable{})
	ecs.Add(wa.sim, e, components.Moveable{Speed: intervalToSpeed(1, 0.05)})
	ecs.Add(wa.sim, e, components.AOI{Radius: 0})
	ecs.Add(wa.sim, e, components.Creature{Kind: components.CreatureRabbit, Threats: map[ecs.Entity]int32{}, HomeX: 5, HomeY: 5, RoamRadius: 0})
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle, FleeHP: 10, HitMemoryTicks: 5})
	addBehaviorTree(wa, e, false)
	ecs.Add(wa.sim, e, interactive.Attacker{AttackRange: 1, AttackDamage: 0})

	// 玩家打一下：20 → 10，触发逃跑
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: e}})
	runActionTicks(wa, 9)
	ai := ecs.Get[components.AI](wa.sim, e)
	if ai.LastHitBy != player {
		t.Fatalf("应记录受击: %d", ai.LastHitBy)
	}
	// 多 tick：状态切 flee 且远离玩家（威胁衰减完之前应保持逃跑）
	movedAway := false
	for i := 0; i < 3; i++ {
		tickWorld(wa)
		ai = ecs.Get[components.AI](wa.sim, e)
		if ai.State != components.CreatureFlee {
			t.Fatalf("低血量应切 flee: state=%v", ai.State)
		}
		p := ecs.Get[components.Position](wa.sim, e)
		if p.X != 5 || p.Y != 5 {
			movedAway = true
		}
	}
	if !movedAway {
		t.Fatal("逃跑生物应远离威胁点")
	}
}

// 敌对生物：狼把兔子视为猎物（HostileKinds），附近没玩家也会追兔子。
func TestCreatureHuntsHostile(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AOIInterval: 1})
	wolf := wa.sim.CreateEntity()
	ecs.Add(wa.sim, wolf, components.Position{X: 0, Y: 0})
	ecs.Add(wa.sim, wolf, components.Health{Cur: 30, Max: 30})
	ecs.Add(wa.sim, wolf, components.Attackable{})
	ecs.Add(wa.sim, wolf, components.Moveable{Speed: intervalToSpeed(1, 0.05)})
	ecs.Add(wa.sim, wolf, components.AOI{Radius: 6})
	ecs.Add(wa.sim, wolf, components.Creature{Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{}, HomeX: 0, HomeY: 0})
	ecs.Add(wa.sim, wolf, components.AI{
		State: components.CreatureIdle, HitMemoryTicks: 5,
		HostileKinds: []components.CreatureKind{components.CreatureRabbit},
	})
	addBehaviorTree(wa, wolf, true)
	ecs.Add(wa.sim, wolf, interactive.Attacker{AttackRange: 1, AttackDamage: 8, AttackCooldown: 5})

	rabbit := wa.sim.CreateEntity()
	ecs.Add(wa.sim, rabbit, components.Position{X: 2, Y: 0})
	ecs.Add(wa.sim, rabbit, components.Health{Cur: 10, Max: 10})
	ecs.Add(wa.sim, rabbit, components.Attackable{})
	ecs.Add(wa.sim, rabbit, components.Moveable{Speed: intervalToSpeed(3, 0.05)})
	ecs.Add(wa.sim, rabbit, components.AOI{Radius: 0})
	ecs.Add(wa.sim, rabbit, components.Creature{Kind: components.CreatureRabbit, Threats: map[ecs.Entity]int32{}, HomeX: 2, HomeY: 0})
	ecs.Add(wa.sim, rabbit, components.AI{State: components.CreatureIdle, FleeHP: 5, HitMemoryTicks: 5})
	addBehaviorTree(wa, rabbit, false)
	ecs.Add(wa.sim, rabbit, interactive.Attacker{AttackRange: 1})

	tickWorld(wa)
	ai := ecs.Get[components.AI](wa.sim, wolf)
	if ai.Target != rabbit || ai.State != components.CreatureChase {
		t.Fatalf("狼应猎杀兔子: target=%d state=%v", ai.Target, ai.State)
	}
	// 狼追上并咬兔子（范围 1）
	for i := 0; i < 12 && ai.Target == rabbit; i++ {
		tickWorld(wa)
		ai = ecs.Get[components.AI](wa.sim, wolf)
	}
	hp := ecs.Get[components.Health](wa.sim, rabbit)
	if hp.Cur >= 10 {
		t.Fatalf("狼追上后应咬到兔子: hp=%d", hp.Cur)
	}
}

// 友好生物（hostile_players=false）：玩家在感知范围内也不主动仇恨/攻击。
func TestCreatureFriendlyToPlayers(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AOIInterval: 1})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 1, Y: 0})

	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: 0, Y: 0})
	ecs.Add(wa.sim, e, components.Health{Cur: 20, Max: 20})
	ecs.Add(wa.sim, e, components.Attackable{})
	ecs.Add(wa.sim, e, components.Moveable{Speed: intervalToSpeed(2, 0.05)})
	ecs.Add(wa.sim, e, components.AOI{Radius: 6})
	ecs.Add(wa.sim, e, components.Creature{Kind: components.CreatureRabbit, Threats: map[ecs.Entity]int32{}, HomeX: 0, HomeY: 0, RoamRadius: 0})
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle, HitMemoryTicks: 5, HostilePlayers: false})
	addBehaviorTree(wa, e, true)
	ecs.Add(wa.sim, e, interactive.Attacker{AttackRange: 1, AttackDamage: 3})

	for i := 0; i < 3; i++ {
		tickWorld(wa)
	}
	ai := ecs.Get[components.AI](wa.sim, e)
	if ai.Target != 0 || ai.State != components.CreatureIdle {
		t.Fatalf("友好生物不应主动攻击玩家: target=%d state=%v", ai.Target, ai.State)
	}
}

// 回归：生物死亡后必须**立刻摘掉碰撞体**，否则尸体会继续卡住玩家。
//
// 真实 bug：尸体默认保留 60 秒（CorpseRetentionTicks=1200），期间实体仍
// "活着"（只挂 Dead 标记）。若不摘碰撞体，死掉的生物会一直挡路——
// 表现为"怪明明死了，走过去还是被卡住"。
func TestDeadCreatureStopsBlocking(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	e := addCreature(wa, 5, 5, 0.4) // 带 Collide 的生物
	tickWorld(wa)
	if !ecs.Has[components.Collide](wa.sim, e) {
		t.Fatal("前置条件：活着的生物应有碰撞体")
	}

	// 杀死它
	hp := ecs.Get[components.Health](wa.sim, e)
	hp.Cur = 0
	ecs.MarkDirty[components.Health](wa.sim, e)
	// 走真实 tick 路径（stampDead 在 onTick 里，tickWorld 不覆盖）
	for i := 0; i < 3; i++ {
		wa.onTick(stubTickCtx{})
	}

	if !ecs.Has[components.Dead](wa.sim, e) {
		t.Fatal("前置条件：应先进入死亡状态")
	}
	if ecs.Has[components.Collide](wa.sim, e) {
		t.Fatal("死亡后应立即摘掉碰撞体（否则尸体会卡住玩家）")
	}
	if ecs.Has[components.Moveable](wa.sim, e) {
		t.Fatal("死亡后应摘掉 Moveable（尸体不该继续占动态层/滑行）")
	}
}

// 回归：摘碰撞体是幂等的，不会重复触发或影响其它实体。
func TestDeadCleanupIsIdempotent(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	dead := addCreature(wa, 5, 5, 0.4)
	alive := addCreature(wa, 9, 9, 0.4)
	tickWorld(wa)

	hp := ecs.Get[components.Health](wa.sim, dead)
	hp.Cur = 0
	ecs.MarkDirty[components.Health](wa.sim, dead)
	// 多跑若干 tick，确认反复进入 stampDead 也不会出问题
	for i := 0; i < 10; i++ {
		wa.onTick(stubTickCtx{})
	}
	if ecs.Has[components.Collide](wa.sim, dead) {
		t.Fatal("死亡实体不该再有碰撞体")
	}
	if !ecs.Has[components.Collide](wa.sim, alive) {
		t.Fatal("存活实体的碰撞体不该被误删")
	}
}

// 回归：尸体保留策略必须**按玩家/NPC 分开**。
//
// 真实 bug（写反了）：原实现是"玩家跳过回收、NPC 保留 CorpseRetentionTicks
// (1200 tick ≈ 60 秒)"——等于玩家尸体永不回收、NPC 拖一分钟，与需求正好相反。
// 正确语义：
//   - 玩家：永久保留（重连要复用同一个实体，由 Offline TTL 单独回收）；
//   - NPC：只留一个短窗口（NpcCorpseRetentionTicks，缺省 200 tick ≈ 10 秒）。
func TestCorpseRetentionSeparatesPlayerAndNpc(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	if wa.cfg.NpcCorpseRetentionTicks <= 0 {
		t.Fatal("NPC 尸体保留时长应有非零缺省值")
	}
	npc := addCreature(wa, 5, 5, 0.4)
	player := wa.createPlayer("u1")
	wa.onTick(stubTickCtx{})

	// 同时杀死 NPC 与玩家
	hpNPC := ecs.Get[components.Health](wa.sim, npc)
	hpNPC.Cur = 0
	ecs.MarkDirty[components.Health](wa.sim, npc)
	hpPlayer := ecs.Get[components.Health](wa.sim, player)
	hpPlayer.Cur = 0
	ecs.MarkDirty[components.Health](wa.sim, player)
	wa.onTick(stubTickCtx{})

	// 跑到超过 NPC 保留时长
	for i := 0; i < wa.cfg.NpcCorpseRetentionTicks+20; i++ {
		wa.onTick(stubTickCtx{})
	}
	if wa.sim.IsAlive(npc) {
		t.Fatal("NPC 尸体应在 NpcCorpseRetentionTicks 之后被回收")
	}
	if !wa.sim.IsAlive(player) {
		t.Fatal("玩家尸体不应被 cleanupCorpses 回收（重连要复用实体）")
	}
}

// 回归：玩家死亡后也必须摘掉碰撞体，不能卡住其他玩家。
func TestDeadPlayerStopsBlocking(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")
	wa.onTick(stubTickCtx{})
	if !ecs.Has[components.Collide](wa.sim, player) {
		t.Fatal("前置条件：活着的玩家应有碰撞体")
	}
	hp := ecs.Get[components.Health](wa.sim, player)
	hp.Cur = 0
	ecs.MarkDirty[components.Health](wa.sim, player)
	wa.onTick(stubTickCtx{})

	if !ecs.Has[components.Dead](wa.sim, player) {
		t.Fatal("前置条件：应先进入死亡状态")
	}
	if ecs.Has[components.Collide](wa.sim, player) {
		t.Fatal("玩家死亡后也应摘掉碰撞体（否则尸体会卡住其他玩家）")
	}
}
