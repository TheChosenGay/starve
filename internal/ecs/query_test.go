package ecs

import "testing"

func TestQueryIteratesDenseOrder(t *testing.T) {
	w := NewWorld()
	e5 := w.CreateEntity()
	w.CreateEntity()
	e2 := w.CreateEntity()
	Add(w, e5, pos{X: 5})
	Add(w, e2, pos{X: 2})

	var got []int
	Query[pos](w, func(e Entity, p *pos) {
		got = append(got, p.X)
	})
	if len(got) != 2 || got[0] != 5 || got[1] != 2 {
		t.Fatalf("Query order = %v, want [5 2]", got)
	}
}

func TestQuery2BothSides(t *testing.T) {
	w := NewWorld()
	for i := 0; i < 5; i++ {
		e := w.CreateEntity()
		Add(w, e, pos{X: i})
		if i%2 == 0 {
			Add(w, e, hp{Cur: i})
		}
	}

	// A=pos(5) 多于 B=hp(3)：遍历 hp，反查 pos
	var gotA []int
	Query2[pos, hp](w, func(e Entity, p *pos, h *hp) {
		gotA = append(gotA, p.X)
	})
	want := []int{0, 2, 4}
	if len(gotA) != 3 {
		t.Fatalf("Query2[pos,hp] len = %d, want 3", len(gotA))
	}
	for i := range want {
		if gotA[i] != want[i] {
			t.Fatalf("Query2[pos,hp] = %v, want %v", gotA, want)
		}
	}

	// 反方向：A=hp(3) 少于 B=pos(5)：遍历 hp
	var gotB []int
	Query2[hp, pos](w, func(e Entity, h *hp, p *pos) {
		gotB = append(gotB, h.Cur)
	})
	if len(gotB) != 3 {
		t.Fatalf("Query2[hp,pos] len = %d, want 3", len(gotB))
	}
	for i := range want {
		if gotB[i] != want[i] {
			t.Fatalf("Query2[hp,pos] = %v, want %v", gotB, want)
		}
	}
}
