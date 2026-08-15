package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
	"starve/internal/game/components/interactive"
)

// 攻击：Attacker ↔ Health 匹配，伤害经 Health.TakeDamage 按防御百分比减免。
func TestAttackDefenseMitigation(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	attacker := wa.sim.CreateEntity()
	ecs.Add(wa.sim, attacker, interactive.Attacker{AttackDamage: 10, AttackRange: 1})
	ecs.Add(wa.sim, attacker, components.Position{X: 0, Y: 0})
	target := wa.sim.CreateEntity()
	ecs.Add(wa.sim, target, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, target, components.Health{Cur: 100, Max: 100, DefensePercent: 50})

	res, ok := interactive.Do(wa.sim, attacker, target, interactive.IntentAttack)
	if !ok || res.DamageDealt != 5 {
		t.Fatalf("防御 50%% 应减免一半: ok=%v dealt=%d", ok, res.DamageDealt)
	}
	if hp := ecs.Get[components.Health](wa.sim, target); hp.Cur != 95 {
		t.Fatalf("Cur = %d, want 95", hp.Cur)
	}

	// 无防御 → 全额受伤
	ecs.Set(wa.sim, target, components.Health{Cur: 100, Max: 100})
	res, ok = interactive.Do(wa.sim, attacker, target, interactive.IntentAttack)
	if !ok || res.DamageDealt != 10 || ecs.Get[components.Health](wa.sim, target).Cur != 90 {
		t.Fatalf("无防御应全额受伤: ok=%v dealt=%d", ok, res.DamageDealt)
	}
}

// 玩家攻击命令：裸手 Attacker 打有防御的目标，走完整命令路径。
func TestPlayerAttackWithDefense(t *testing.T) {
	wa := NewWorldActor(WorldConfig{AttackDamage: 10})
	player := wa.createPlayer("u1")
	ecs.Set(wa.sim, player, components.Position{X: 0, Y: 0})
	target := wa.sim.CreateEntity()
	ecs.Add(wa.sim, target, components.Position{X: 0, Y: 1})
	ecs.Add(wa.sim, target, components.Health{Cur: 100, Max: 100, DefensePercent: 20})

	wa.cmds.Handle(Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: player, Target: target}})
	hp := ecs.Get[components.Health](wa.sim, target)
	if hp.Cur != 92 {
		t.Fatalf("玩家攻击后 Cur = %d, want 92（10 × 80%%）", hp.Cur)
	}
}

// 装备防御：Defense 组件生命周期——挂上自动叠加 Health.DefensePercent，移除自动扣减。
func TestEquipDefense(t *testing.T) {
	wa := NewWorldActor(WorldConfig{})
	player := wa.createPlayer("u1")

	ecs.Add(wa.sim, player, components.Defense{Percent: 30})
	hp := ecs.Get[components.Health](wa.sim, player)
	if hp.DefensePercent != 30 {
		t.Fatalf("装备后防御 = %d, want 30", hp.DefensePercent)
	}

	ecs.Remove[components.Defense](wa.sim, player)
	if hp.DefensePercent != 0 {
		t.Fatalf("卸下后防御 = %d, want 0", hp.DefensePercent)
	}
}
