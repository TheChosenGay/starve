package systems

import (
	"testing"
	"time"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// 回归：**移动中的实体必须每 tick 都标脏 Moveable**（连续性契约）。
//
// 真实 bug：提交阶段只判 `ApplyDisplacement` 的返回值（= 是否跨格），
// 于是"移动中但不跨格"的 tick 不下发。10 格/秒 ÷ 20Hz = 0.5 格/tick，
// 即每两个 tick 才跨一格 → 客户端只拿到 10Hz 的有效位置更新，
// 而渲染是 60FPS，表现为走动时一卡一跳（实测 61% 的渲染帧位置不变）。
func TestMovingEntityMarkedDirtyEveryTick(t *testing.T) {
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)

	e := w.CreateEntity()
	ecs.Add(w, e, components.Position{X: 10, Y: 10})
	// 10 格/秒：每 tick 走 0.5 格，即**每两个 tick 才跨一格**
	ecs.Add(w, e, components.Moveable{Speed: 10, EffectiveSpeed: 10, DirX: 1})

	mvID := ecs.ComponentIDOf[components.Moveable](w)
	ms := &MoveSystem{}
	ticks := 40
	dirtyCount := 0
	for i := 0; i < ticks; i++ {
		w.DrainDirty()
		ms.Update(w, 50*time.Millisecond)
		for _, ids := range w.DrainDirty() {
			for _, id := range ids {
				if id == mvID {
					dirtyCount++
				}
			}
		}
	}
	// 每个 tick 都在移动 → 应该每次标脏（允许首 tick 未建立等问题，留 10% 余量）
	if dirtyCount < ticks*9/10 {
		t.Fatalf("移动中的实体应每 tick 标脏 Moveable：%d/%d 次"+
			"（旧实现只在不跨格的 tick 漏发，约 50%%）", dirtyCount, ticks)
	}
}

// 对照：停止的实体不应每 tick 标脏（避免无谓流量）。
func TestStoppedEntityNotMarkedDirty(t *testing.T) {
	w := ecs.NewWorld()
	components.RegisterCodecs(w, false)

	e := w.CreateEntity()
	ecs.Add(w, e, components.Position{X: 10, Y: 10})
	ecs.Add(w, e, components.Moveable{Speed: 10, EffectiveSpeed: 10}) // 无方向 = 停

	mvID := ecs.ComponentIDOf[components.Moveable](w)
	ms := &MoveSystem{}
	for i := 0; i < 3; i++ {
		ms.Update(w, 50*time.Millisecond)
		w.DrainDirty()
	}
	dirty := 0
	for i := 0; i < 20; i++ {
		ms.Update(w, 50*time.Millisecond)
		for _, ids := range w.DrainDirty() {
			for _, id := range ids {
				if id == mvID {
					dirty++
				}
			}
		}
	}
	if dirty > 2 {
		t.Fatalf("静止实体不该持续标脏 Moveable：%d/20 次", dirty)
	}
}
