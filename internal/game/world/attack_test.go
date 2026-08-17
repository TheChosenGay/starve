package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// 攻击：Attacker ↔ Attackable 匹配；伤害由 Attackable 依赖 Defense/Health 结算。
func TestAttackDefenseMitigation(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	attacker := wa.sim.CreateEntity()
	ecs.Add(wa.sim, attacker, interactive.Attacker{AttackDamage: 10, AttackRange: 1})
	ecs.Add(wa.sim, attacker, components.Position{X: 0, Y: 0})
	target := wa.sim.CreateEntity()
	ecs.Add(wa.sim, target, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, target, components.Health{Cur: 100, Max: 100})
	ecs.Add(wa.sim, target, components.Attackable{})
	ecs.Add(wa.sim, target, components.Defense{Percent: 50})

	if !interactive.Do(wa.sim, attacker, target, interactive.IntentAttack) {
		t.Fatal("防御 50%% 的攻击应执行")
	}
	if hp := ecs.Get[components.Health](wa.sim, target); hp.Cur != 95 {
		t.Fatalf("Cur = %d, want 95", hp.Cur)
	}

	// 无防御 → 全额受伤
	ecs.Remove[components.Defense](wa.sim, target)
	ecs.Set(wa.sim, target, components.Health{Cur: 100, Max: 100})
	if !interactive.Do(wa.sim, attacker, target, interactive.IntentAttack) {
		t.Fatal("无防御的攻击应执行")
	}
	if hp := ecs.Get[components.Health](wa.sim, target); hp.Cur != 90 {
		t.Fatalf("无防御 Cur = %d, want 90", hp.Cur)
	}
}

// 玩家攻击命令：裸手 Attacker 打有防御（Attackable+Defense）的目标，走完整命令路径。
func TestPlayerAttackWithDefense(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AttackDamage: 10})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	target := wa.sim.CreateEntity()
	ecs.Add(wa.sim, target, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, target, components.Health{Cur: 100, Max: 100})
	ecs.Add(wa.sim, target, components.Attackable{})
	ecs.Add(wa.sim, target, components.Defense{Percent: 20})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: target}})
	hp := ecs.Get[components.Health](wa.sim, target)
	if hp.Cur != 92 {
		t.Fatalf("玩家攻击后 Cur = %d, want 92（10 × 80%%）", hp.Cur)
	}
}

// 防御组件：挂上 Defense 后攻击减免生效，移除后恢复全额。
func TestDefenseComponent(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	attacker := wa.sim.CreateEntity()
	ecs.Add(wa.sim, attacker, interactive.Attacker{AttackDamage: 10, AttackRange: 1})
	ecs.Add(wa.sim, attacker, components.Position{X: 0, Y: 0})
	target := wa.sim.CreateEntity()
	ecs.Add(wa.sim, target, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, target, components.Health{Cur: 100, Max: 100})
	ecs.Add(wa.sim, target, components.Attackable{})

	// 无防御：10 点全伤
	if !interactive.Do(wa.sim, attacker, target, interactive.IntentAttack) {
		t.Fatal("无防御应可攻击")
	}
	if hp := ecs.Get[components.Health](wa.sim, target); hp.Cur != 90 {
		t.Fatalf("无防御 Cur = %d, want 90", hp.Cur)
	}
	// 挂防御 30%：10 伤 → 7
	ecs.Add(wa.sim, target, components.Defense{Percent: 30})
	interactive.Do(wa.sim, attacker, target, interactive.IntentAttack)
	if hp := ecs.Get[components.Health](wa.sim, target); hp.Cur != 83 {
		t.Fatalf("有防御 Cur = %d, want 83", hp.Cur)
	}
}
