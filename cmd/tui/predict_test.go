package main

import (
	"testing"

	game "starve/pkg/proto/game"
)

// 回归锁：TUI 每收到一次推送就 Sync 一次，重复 Sync 必须不 panic。
//
// 历史 bug（线上崩溃）：Sync 内部走 WorldSnapshot.AddDynamic / SetMap，
// 两者都用 ecs.Add / AddResource —— 这两个 API 对"已存在"会 panic，
// 于是**第一次**收到推送正常、**第二次**直接崩。修法是改成幂等 upsert。
func TestPredictorSyncRepeated(t *testing.T) {
	w := newWorld()
	w.width, w.height = 16, 16
	w.cornerTypes = make([]byte, 17*17)
	w.cornerHeights = make([]byte, 17*17)
	for i := range w.cornerTypes {
		w.cornerTypes[i] = 3
	}
	own := &entity{id: 1}
	own.pos = &game.Position{X: 4, Y: 4}
	own.moveable = &game.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5}
	own.collide = &game.Collide{Shape: game.CollideShape_COLLIDE_SHAPE_CAPSULE, Radius: 0.305}
	own.player = &game.Player{}
	w.entities[1] = own
	w.own = 1
	w.haveCfg, w.haveSnap = true, true

	// 一只动物（动态）+ 一棵树（静态）
	mob := &entity{id: 2}
	mob.pos = &game.Position{X: 8, Y: 4}
	mob.moveable = &game.Moveable{Speed: 10, SubX: 0.5, SubY: 0.5}
	mob.collide = &game.Collide{Shape: game.CollideShape_COLLIDE_SHAPE_CAPSULE, Radius: 0.3}
	mob.creature = &game.Creature{}
	w.entities[2] = mob

	tree := &entity{id: 3}
	tree.pos = &game.Position{X: 6, Y: 6}
	tree.collide = &game.Collide{Shape: game.CollideShape_COLLIDE_SHAPE_CIRCLE, Radius: 0.28}
	w.entities[3] = tree

	p := newPredictor()
	p.Sync(w)
	// 模拟持续推送 + 按住方向键走路：每次 Sync 都应安全（这里是崩溃点）
	p.SetIntent(1, 0)
	for i := 0; i < 10; i++ {
		p.Sync(w)
		p.Tick(0.05)
	}
	x, y := p.Position()
	t.Logf("10 次 Sync 后 active=%v pos=(%.3f,%.3f)", p.active, x, y)
	// 按住 +X 走了 10 帧 × 50ms × 10 格/秒 = 5 格，但被 (8,4) 的动物挤开，
	// 所以 x 应明显推进、y 应有偏移（证明 ORCA 参与了本地预测）。
	if x < 6.0 {
		t.Fatalf("预测应显著推进，实际 x=%.3f", x)
	}
	if y == 4.5 {
		t.Fatalf("绕过动物时应产生横向偏移，实际 y=%.3f", y)
	}
	if !p.active {
		t.Fatal("预测器应处于激活状态")
	}
	_ = x
}
