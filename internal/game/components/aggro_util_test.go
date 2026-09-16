package components

import (
	"testing"

	"starve/internal/ecs"
)

// SpawnAggroCapable / AddAggroCapable 的契约测试。
//
// 重点不是"函数能跑"，而是"照它生成的实体**真的能参与群体仇恨**"——
// 这正是这个辅助函数存在的意义，也是漏挂组件时最容易出错的地方。

func TestSpawnAggroCapableHasFullComponentSet(t *testing.T) {
	w := newAggroWorld()

	e := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 10, Y: 10,
		HP: 30, Perception: 6, Threat: 16,
	})

	if missing := MissingAggroComponents(w, e); len(missing) != 0 {
		t.Fatalf("生成的实体应具备完整能力，缺 %v", missing)
	}
	if !HasAggroCapability(w, e) {
		t.Fatal("HasAggroCapability 应为 true")
	}

	// 字段是否正确落到组件上（不是"挂了但值不对"）
	pos := ecs.Get[Position](w, e)
	if pos.X != 10 || pos.Y != 10 {
		t.Fatalf("Position 应落到 (10,10)，实际 (%d,%d)", pos.X, pos.Y)
	}
	hp := ecs.Get[Health](w, e)
	if hp.Cur != 30 || hp.Max != 30 {
		t.Fatalf("Health 应为 30/30，实际 %d/%d", hp.Cur, hp.Max)
	}
	aoi := ecs.Get[AOI](w, e)
	if aoi.Perception != 6 || aoi.Threat != 16 {
		t.Fatalf("AOI 半径应为 per=6 thr=16，实际 per=%d thr=%d", aoi.Perception, aoi.Threat)
	}
	// 覆盖半径必须取两者较大者（AOI 只查一份）
	if aoi.Radius != 16 {
		t.Fatalf("AOI.Radius 应为 max(6,16)=16，实际 %d", aoi.Radius)
	}
	if c := ecs.Get[Creature](w, e); c.Kind != CreatureWolf {
		t.Fatalf("Kind 应为 Wolf，实际 %v", c.Kind)
	}
}

// 核心契约：用辅助函数造出的实体，**真的能被传播、也能传播出去**。
func TestSpawnedCreatureCanPropagate(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 10, Y: 10, HP: 50, Perception: 6, Threat: 16,
	})
	ally := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 12, Y: 10, HP: 30, Perception: 6, Threat: 16,
	})

	// AOI.Visible 平时由 AOISystem 填；这里手工设（测试环境不跑系统）
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{player, ally}

	Attackable{}.ApplyDamage(w, victim, player, 10)

	if got := ecs.Get[Creature](w, victim).DirectTarget(); got != player {
		t.Fatal("受害者应获得直接仇恨")
	}
	if _, ok := ecs.Get[Creature](w, ally).Indirect[player]; !ok {
		t.Fatal("同类同伴应获得间接仇恨（辅助函数生成的实体必须真的能传播）")
	}
}

// 异类不传播：辅助函数生成的实体也同样遵守同类过滤。
func TestSpawnedCreaturesOnlySpreadToSameKind(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	wolf := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 10, Y: 10, HP: 50, Perception: 6, Threat: 16,
	})
	boar := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureBoar, X: 12, Y: 10, HP: 50, Perception: 7, Threat: 14,
	})

	ecs.Get[AOI](w, wolf).Visible = []ecs.Entity{player, boar}
	Attackable{}.ApplyDamage(w, wolf, player, 10)

	if n := len(ecs.Get[Creature](w, boar).Indirect); n != 0 {
		t.Fatalf("异类不应获得仇恨，实际 %d 条", n)
	}
}

// 传 Threat 半径而非 Radius：远超出 Threat 的同类不该被通知。
func TestSpawnedCreatureUsesThreatRadius(t *testing.T) {
	w := newAggroWorld()
	player := w.CreateEntity()
	ecs.Add(w, player, Position{X: 0, Y: 0})

	victim := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 10, Y: 10, HP: 50,
		Perception: 6, Threat: 4, // 传播半径只有 4
	})
	// 覆盖半径 6 内的同类（距离 5）：在 Visible 里，但超出 Threat(4)
	edge := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 15, Y: 10, HP: 30, Perception: 6, Threat: 4,
	})
	ecs.Get[AOI](w, victim).Visible = []ecs.Entity{player, edge}

	Attackable{}.ApplyDamage(w, victim, player, 10)
	if n := len(ecs.Get[Creature](w, edge).Indirect); n != 0 {
		t.Fatalf("超出 Threat 半径(4) 的同类不该被通知，实际 %d 条", n)
	}
}

// 半径为 0 时不挂 AOI：该生物**没有**仇恨能力（显式选择，不是默认）。
func TestSpawnWithoutRadiiHasNoAOI(t *testing.T) {
	w := newAggroWorld()
	e := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 5, Y: 5, HP: 10,
		// Perception/Threat 都为 0
	})
	if ecs.Has[AOI](w, e) {
		t.Fatal("两个半径都为 0 时不应挂 AOI")
	}
	if HasAggroCapability(w, e) {
		t.Fatal("没有 AOI 就不具备完整仇恨能力")
	}
	if missing := MissingAggroComponents(w, e); len(missing) != 1 || missing[0] != "AOI" {
		t.Fatalf("应恰好缺 AOI，实际 %v", missing)
	}
}

// 只配其中一个半径时，另一个按 0 处理，但覆盖半径仍取有效值。
func TestSpawnWithOnlyThreatRadius(t *testing.T) {
	w := newAggroWorld()
	e := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 5, Y: 5, HP: 10,
		Threat: 12, // 只配传播半径（不感知，但会被通知/传播）
	})
	aoi := ecs.Get[AOI](w, e)
	if aoi.Radius != 12 {
		t.Fatalf("覆盖半径应为 12，实际 %d", aoi.Radius)
	}
	if aoi.PerceptionRadius() != 12 {
		// Perception=0 时回退到 Radius（兼容语义，见 AOI.PerceptionRadius）
		t.Fatalf("Perception 未配时应回退到 Radius，实际 %d", aoi.PerceptionRadius())
	}
	if aoi.ThreatRadius() != 12 {
		t.Fatalf("ThreatRadius 应为 12，实际 %d", aoi.ThreatRadius())
	}
}

// AddAggroCapable 给已存在的实体补能力（与 Spawn 分开的场景）。
func TestAddAggroCapableToExistingEntity(t *testing.T) {
	w := newAggroWorld()
	e := w.CreateEntity() // 先建实体（比如读档恢复）
	AddAggroCapable(w, e, AggroCapable{
		Kind: CreatureWolf, X: 3, Y: 4, HP: 20, Perception: 5, Threat: 9,
	})
	if missing := MissingAggroComponents(w, e); len(missing) != 0 {
		t.Fatalf("补完后应完整，缺 %v", missing)
	}
	if hp := ecs.Get[Health](w, e); hp.Cur != 20 {
		t.Fatalf("Health 应为 20，实际 %d", hp.Cur)
	}
}

// MaxHP 为 0 时按 HP 补齐（避免造出 0/0 的"打不死的怪"）。
func TestSpawnFillsMaxHPWhenZero(t *testing.T) {
	w := newAggroWorld()
	e := SpawnAggroCapable(w, AggroCapable{
		Kind: CreatureWolf, X: 1, Y: 1, HP: 25, // MaxHP 留空
		Perception: 4, Threat: 8,
	})
	hp := ecs.Get[Health](w, e)
	if hp.Cur != 25 || hp.Max != 25 {
		t.Fatalf("MaxHP 应回填为 25，实际 %d/%d", hp.Cur, hp.Max)
	}
}

// 自检辅助本身要准：故意造缺组件的实体，MissingAggroComponents 必须报对。
func TestMissingAggroComponentsReportsAccurately(t *testing.T) {
	w := newAggroWorld()
	e := w.CreateEntity()
	ecs.Add(w, e, Position{X: 1, Y: 1})
	ecs.Add(w, e, Health{Cur: 10, Max: 10})

	missing := MissingAggroComponents(w, e)
	want := map[string]bool{"Attackable": true, "Creature": true, "AOI": true}
	if len(missing) != len(want) {
		t.Fatalf("应缺 3 项，实际 %v", missing)
	}
	for _, m := range missing {
		if !want[m] {
			t.Fatalf("不该报缺失 %q（完整列表 %v）", m, missing)
		}
	}
}

// 自检函数必须**逐个组件**都准：缺任意一个都要报 false / 报出名字。
//
// 为什么单独测：HasAggroCapability/MissingAggroComponents 正是给人用来
// "防漏挂"的。如果它们自己会误报 true（假阴性），就完全失去意义——
// 而"齐全时返回 true"这类测试发现不了这种错。
// （变异测试证实过：从 HasAggroCapability 里删掉某一项检查，只有本测试能抓到。）
func TestSelfCheckDetectsEachMissingComponent(t *testing.T) {
	type comp struct {
		name string
		add  func(w *ecs.World, e ecs.Entity)
	}
	all := []comp{
		{"Position", func(w *ecs.World, e ecs.Entity) { ecs.Add(w, e, Position{X: 1, Y: 1}) }},
		{"Health", func(w *ecs.World, e ecs.Entity) { ecs.Add(w, e, Health{Cur: 10, Max: 10}) }},
		{"Attackable", func(w *ecs.World, e ecs.Entity) { ecs.Add(w, e, Attackable{}) }},
		{"Creature", func(w *ecs.World, e ecs.Entity) {
			ecs.Add(w, e, Creature{Kind: CreatureWolf, Threats: map[ecs.Entity]int32{}})
		}},
		{"AOI", func(w *ecs.World, e ecs.Entity) {
			ecs.Add(w, e, AOI{Radius: 8, Perception: 6, Threat: 8})
		}},
	}

	for skip := range all {
		w := newAggroWorld()
		e := w.CreateEntity()
		for i, c := range all {
			if i == skip {
				continue // 故意漏挂一个
			}
			c.add(w, e)
		}

		if HasAggroCapability(w, e) {
			t.Fatalf("漏挂 %s 时 HasAggroCapability 必须为 false（否则自检会假阴性）",
				all[skip].name)
		}
		missing := MissingAggroComponents(w, e)
		if len(missing) != 1 || missing[0] != all[skip].name {
			t.Fatalf("漏挂 %s 时应恰好报出它，实际 %v", all[skip].name, missing)
		}
	}

	// 齐全时必须为 true（对照，避免"永远返回 false"这种反向错误）
	w := newAggroWorld()
	e := w.CreateEntity()
	for _, c := range all {
		c.add(w, e)
	}
	if !HasAggroCapability(w, e) {
		t.Fatal("组件齐全时必须为 true")
	}
	if missing := MissingAggroComponents(w, e); len(missing) != 0 {
		t.Fatalf("齐全时不应报缺失，实际 %v", missing)
	}
}
