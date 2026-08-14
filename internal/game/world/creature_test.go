package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/worldmap"
	game "starve/pkg/proto/game"
)

// addWolf 摆一只狼（主动：仇恨/追击/攻击）。
func addWolf(t *testing.T, wa *WorldActor, x, y int) ecs.Entity {
	t.Helper()
	e := wa.sim.CreateEntity()
	ecs.Add(wa.sim, e, components.Position{X: x, Y: y})
	ecs.Add(wa.sim, e, components.Health{Cur: 30, Max: 30})
	ecs.Add(wa.sim, e, components.Moveable{Interval: 1})
	ecs.Add(wa.sim, e, components.AOI{Radius: 6})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{}, HomeX: x, HomeY: y, RoamRadius: 0,
		Drops: []components.ItemStack{{Kind: components.ItemMeat, Count: 2}},
	})
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle, HitMemoryTicks: 5, HostilePlayers: true})
	ecs.Add(wa.sim, e, components.Weapon{AttackRange: 1, AttackDamage: 8, AttackCooldown: 5})
	return e
}

// 仇恨 + 追击 + 攻击：玩家进入感知半径 → 狼锁定并攻击，玩家掉血。
func TestCreatureAggroChaseAttack(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	wolf := addWolf(t, wa, 0, 0)
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	hp := ecs.Get[components.Health](wa.sim, player)

	tickWorld(wa) // 感知 → 目标 → 攻击（同格，范围 1）
	ai := ecs.Get[components.AI](wa.sim, wolf)
	if ai.Target != player || ai.State != components.CreatureAttack {
		t.Fatalf("狼应锁定玩家并攻击: target=%d state=%v", ai.Target, ai.State)
	}
	if hp.Cur != 92 {
		t.Fatalf("狼攻击应扣 8 血: hp=%d want 92", hp.Cur)
	}
	// 冷却 5 tick，再打一轮
	for i := 0; i < 6; i++ {
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

// 死亡掉落：狼死亡 → Dead + Loot（移除 Creature）→ 拾取进背包。
func TestCreatureDeathDrops(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	player := createPlayer(t, eng, pid, "u1")
	syncWorld(t, eng, pid)
	wolf := addWolf(t, wa, 0, 0)
	syncWorld(t, eng, pid)

	// 玩家攻击 4 次（伤害 10）→ 狼 30 血归零
	for i := 0; i < 4; i++ {
		eng.Send(pid, Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: wolf}})
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if !ecs.Has[components.Dead](wa.sim, wolf) {
		t.Fatal("狼应死亡")
	}
	if ecs.Has[components.Creature](wa.sim, wolf) {
		t.Fatal("死亡后应移除 Creature 组件")
	}
	if !ecs.Has[components.Loot](wa.sim, wolf) {
		t.Fatal("死亡应生成 Loot")
	}

	// 拾取 → 背包肉 +2
	eng.Send(pid, Command{UID: "u1", Kind: CommandPickup, Data: PickupData{Player: player, Target: wolf}})
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
	ecs.Add(wa.sim, e, components.Moveable{Interval: 1})
	ecs.Add(wa.sim, e, components.AOI{Radius: 0})
	ecs.Add(wa.sim, e, components.Creature{
		Kind: components.CreatureRabbit, Threats: map[ecs.Entity]int32{}, HomeX: 10, HomeY: 10, RoamRadius: 6,
	})
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle, HitMemoryTicks: 5})
	ecs.Add(wa.sim, e, components.Weapon{AttackRange: 1})

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
	ecs.Add(wa.sim, e, components.Moveable{Interval: 1})
	ecs.Add(wa.sim, e, components.AOI{Radius: 0})
	ecs.Add(wa.sim, e, components.Creature{Kind: components.CreatureRabbit, Threats: map[ecs.Entity]int32{}, HomeX: 5, HomeY: 5, RoamRadius: 0})
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle, FleeHP: 10, HitMemoryTicks: 5})
	ecs.Add(wa.sim, e, components.Weapon{AttackRange: 1, AttackDamage: 0})

	// 玩家打一下：20 → 10，触发逃跑
	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: e}})
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
	ecs.Add(wa.sim, wolf, components.Moveable{Interval: 1})
	ecs.Add(wa.sim, wolf, components.AOI{Radius: 6})
	ecs.Add(wa.sim, wolf, components.Creature{Kind: components.CreatureWolf, Threats: map[ecs.Entity]int32{}, HomeX: 0, HomeY: 0})
	ecs.Add(wa.sim, wolf, components.AI{
		State: components.CreatureIdle, HitMemoryTicks: 5,
		HostileKinds: []components.CreatureKind{components.CreatureRabbit},
	})
	ecs.Add(wa.sim, wolf, components.Weapon{AttackRange: 1, AttackDamage: 8, AttackCooldown: 5})

	rabbit := wa.sim.CreateEntity()
	ecs.Add(wa.sim, rabbit, components.Position{X: 2, Y: 0})
	ecs.Add(wa.sim, rabbit, components.Health{Cur: 10, Max: 10})
	ecs.Add(wa.sim, rabbit, components.Moveable{Interval: 3})
	ecs.Add(wa.sim, rabbit, components.AOI{Radius: 0})
	ecs.Add(wa.sim, rabbit, components.Creature{Kind: components.CreatureRabbit, Threats: map[ecs.Entity]int32{}, HomeX: 2, HomeY: 0})
	ecs.Add(wa.sim, rabbit, components.AI{State: components.CreatureIdle, FleeHP: 5, HitMemoryTicks: 5})
	ecs.Add(wa.sim, rabbit, components.Weapon{AttackRange: 1})

	tickWorld(wa)
	ai := ecs.Get[components.AI](wa.sim, wolf)
	if ai.Target != rabbit || ai.State != components.CreatureChase {
		t.Fatalf("狼应猎杀兔子: target=%d state=%v", ai.Target, ai.State)
	}
	// 狼追上并咬兔子（范围 1）
	for i := 0; i < 4 && ai.Target == rabbit; i++ {
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
	ecs.Add(wa.sim, e, components.Moveable{Interval: 2})
	ecs.Add(wa.sim, e, components.AOI{Radius: 6})
	ecs.Add(wa.sim, e, components.Creature{Kind: components.CreatureRabbit, Threats: map[ecs.Entity]int32{}, HomeX: 0, HomeY: 0, RoamRadius: 0})
	ecs.Add(wa.sim, e, components.AI{State: components.CreatureIdle, HitMemoryTicks: 5, HostilePlayers: false})
	ecs.Add(wa.sim, e, components.Weapon{AttackRange: 1, AttackDamage: 3})

	for i := 0; i < 3; i++ {
		tickWorld(wa)
	}
	ai := ecs.Get[components.AI](wa.sim, e)
	if ai.Target != 0 || ai.State != components.CreatureIdle {
		t.Fatalf("友好生物不应主动攻击玩家: target=%d state=%v", ai.Target, ai.State)
	}
}
