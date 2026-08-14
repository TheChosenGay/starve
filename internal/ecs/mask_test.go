package ecs

import "testing"

// 掩码多字：位号超过 64 自动扩容，已有实体的掩码完整迁移保留。
func TestMaskGrowMultiWord(t *testing.T) {
	w := NewWorld()
	e1 := w.CreateEntity()
	e2 := w.CreateEntity()
	w.maskSet(e1, 5, true)
	w.maskSet(e2, 7, true)

	w.maskSet(e2, 64, true)  // 触发 1 → 2 字
	w.maskSet(e2, 130, true) // 触发 2 → 3 字

	if w.maskWords != 3 {
		t.Fatalf("maskWords = %d, want 3", w.maskWords)
	}
	m1, m2 := w.maskAt(e1), w.maskAt(e2)
	if len(m1) != 3 || len(m2) != 3 {
		t.Fatalf("掩码长度 = %d/%d, want 3/3", len(m1), len(m2))
	}
	if m1[0]&(1<<5) == 0 || m1[1] != 0 || m1[2] != 0 {
		t.Fatal("e1 掩码应保留且无脏位")
	}
	if m2[0]&(1<<7) == 0 || m2[1]&1 == 0 || m2[2]&(1<<2) == 0 {
		t.Fatal("e2 跨字位应正确")
	}
	w.maskSet(e2, 64, false)
	if m2[1]&1 != 0 {
		t.Fatal("清位失败")
	}
}

// 掩码清位不影响其他实体/其他字。
func TestMaskClearIsolated(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	w.maskSet(e, 0, true)
	w.maskSet(e, 70, true)
	w.maskSet(e, 0, false)

	m := w.maskAt(e)
	if m[0]&1 != 0 {
		t.Fatal("bit0 应已清除")
	}
	if m[1]&(1<<6) == 0 {
		t.Fatal("bit70 应保留")
	}
}
