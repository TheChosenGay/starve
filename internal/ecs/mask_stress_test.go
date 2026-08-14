package ecs

import (
	"math/rand"
	"testing"
)

// 压测组件：无钩子，纯数据。
type sA struct{}
type sB struct{}
type sC struct{}

// 随机操作对账模型：固定种子下随机 创建/加组件/移除组件/销毁，
// 每一步后把 ECS 状态与模型比对（Has 结果 + 销毁后全清 + 实体计数）。
// 销毁走掩码路径，任何掩码位错误都会在这里暴露。
func TestMaskStressModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	w := NewWorld()

	type entState struct {
		a, b, c bool
	}
	model := make(map[Entity]entState)
	ids := []Entity{}

	verify := func() {
		for _, e := range ids {
			st := model[e]
			if Has[sA](w, e) != st.a || Has[sB](w, e) != st.b || Has[sC](w, e) != st.c {
				t.Fatalf("实体 %d 状态不一致: model=%+v", e, st)
			}
		}
		if w.EntityCount() != len(model) {
			t.Fatalf("实体数 = %d, want %d", w.EntityCount(), len(model))
		}
	}

	for op := 0; op < 5000; op++ {
		switch op % 5 {
		case 0: // 创建
			if len(ids) < 40 {
				e := w.CreateEntity()
				model[e] = entState{}
				ids = append(ids, e)
			}
		case 1, 2, 3: // 加/移除 A/B/C
			if len(ids) == 0 {
				continue
			}
			e := ids[rng.Intn(len(ids))]
			st := model[e]
			switch op % 5 {
			case 1:
				if !st.a {
					Add(w, e, sA{})
					st.a = true
				} else {
					Remove[sA](w, e)
					st.a = false
				}
			case 2:
				if !st.b {
					Add(w, e, sB{})
					st.b = true
				} else {
					Remove[sB](w, e)
					st.b = false
				}
			case 3:
				if !st.c {
					Add(w, e, sC{})
					st.c = true
				} else {
					Remove[sC](w, e)
					st.c = false
				}
			}
			model[e] = st
		case 4: // 销毁
			if len(ids) == 0 {
				continue
			}
			i := rng.Intn(len(ids))
			e := ids[i]
			w.DestroyEntity(e)
			// 销毁必须全清（掩码驱动，错一位就残留）
			if Has[sA](w, e) || Has[sB](w, e) || Has[sC](w, e) || w.IsAlive(e) {
				t.Fatalf("实体 %d 销毁后残留组件/存活", e)
			}
			delete(model, e)
			ids = append(ids[:i], ids[i+1:]...)
		}
		if op%50 == 0 {
			verify()
		}
	}
	verify()
}
