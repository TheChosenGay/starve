package ecs

import "testing"

// 65 种组件（bit 0..64），正好跨过 64 边界进入第二个掩码字。
type (
	comp00 struct{}
	comp01 struct{}
	comp02 struct{}
	comp03 struct{}
	comp04 struct{}
	comp05 struct{}
	comp06 struct{}
	comp07 struct{}
	comp08 struct{}
	comp09 struct{}
	comp10 struct{}
	comp11 struct{}
	comp12 struct{}
	comp13 struct{}
	comp14 struct{}
	comp15 struct{}
	comp16 struct{}
	comp17 struct{}
	comp18 struct{}
	comp19 struct{}
	comp20 struct{}
	comp21 struct{}
	comp22 struct{}
	comp23 struct{}
	comp24 struct{}
	comp25 struct{}
	comp26 struct{}
	comp27 struct{}
	comp28 struct{}
	comp29 struct{}
	comp30 struct{}
	comp31 struct{}
	comp32 struct{}
	comp33 struct{}
	comp34 struct{}
	comp35 struct{}
	comp36 struct{}
	comp37 struct{}
	comp38 struct{}
	comp39 struct{}
	comp40 struct{}
	comp41 struct{}
	comp42 struct{}
	comp43 struct{}
	comp44 struct{}
	comp45 struct{}
	comp46 struct{}
	comp47 struct{}
	comp48 struct{}
	comp49 struct{}
	comp50 struct{}
	comp51 struct{}
	comp52 struct{}
	comp53 struct{}
	comp54 struct{}
	comp55 struct{}
	comp56 struct{}
	comp57 struct{}
	comp58 struct{}
	comp59 struct{}
	comp60 struct{}
	comp61 struct{}
	comp62 struct{}
	comp63 struct{}
	comp64 struct{}
)

// 首尾两个组件带生命周期钩子，验证跨字销毁顺序：word0（bit0）先于 word1（bit64）。
var largeDestroyOrder []int

func (comp00) OnRemove(w *World, e Entity) { largeDestroyOrder = append(largeDestroyOrder, 0) }
func (comp64) OnRemove(w *World, e Entity) { largeDestroyOrder = append(largeDestroyOrder, 64) }

// 跨 64 边界：65 种组件全部挂到实体，销毁后所有存储清空、掩码归零、
// 生命周期钩子按位号升序跨字触发。
func TestMaskLargeWorldDestroy(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	add65(w, e)
	if w.maskWords != 2 {
		t.Fatalf("maskWords = %d, want 2（65 种组件跨 64 边界）", w.maskWords)
	}
	if !Has[comp00](w, e) || !Has[comp64](w, e) {
		t.Fatal("首尾组件应已挂载")
	}

	largeDestroyOrder = nil
	w.DestroyEntity(e)

	if len(largeDestroyOrder) != 2 || largeDestroyOrder[0] != 0 || largeDestroyOrder[1] != 64 {
		t.Fatalf("跨字销毁顺序 = %v, want [0 64]", largeDestroyOrder)
	}
	for _, typ := range w.storageOrder {
		if st := w.storages[typ].(storageLike); st.hasEntity(e) {
			t.Fatalf("存储 %v 销毁后仍持有实体", typ)
		}
	}
	for _, word := range w.maskAt(e) {
		if word != 0 {
			t.Fatal("销毁后掩码应全零")
		}
	}
	if w.IsAlive(e) {
		t.Fatal("实体应已销毁")
	}
}

// add65 按序挂载 65 种组件（位号 = 挂载顺序，正好跨过 64 边界）。
func add65(w *World, e Entity) {
	Add(w, e, comp00{})
	Add(w, e, comp01{})
	Add(w, e, comp02{})
	Add(w, e, comp03{})
	Add(w, e, comp04{})
	Add(w, e, comp05{})
	Add(w, e, comp06{})
	Add(w, e, comp07{})
	Add(w, e, comp08{})
	Add(w, e, comp09{})
	Add(w, e, comp10{})
	Add(w, e, comp11{})
	Add(w, e, comp12{})
	Add(w, e, comp13{})
	Add(w, e, comp14{})
	Add(w, e, comp15{})
	Add(w, e, comp16{})
	Add(w, e, comp17{})
	Add(w, e, comp18{})
	Add(w, e, comp19{})
	Add(w, e, comp20{})
	Add(w, e, comp21{})
	Add(w, e, comp22{})
	Add(w, e, comp23{})
	Add(w, e, comp24{})
	Add(w, e, comp25{})
	Add(w, e, comp26{})
	Add(w, e, comp27{})
	Add(w, e, comp28{})
	Add(w, e, comp29{})
	Add(w, e, comp30{})
	Add(w, e, comp31{})
	Add(w, e, comp32{})
	Add(w, e, comp33{})
	Add(w, e, comp34{})
	Add(w, e, comp35{})
	Add(w, e, comp36{})
	Add(w, e, comp37{})
	Add(w, e, comp38{})
	Add(w, e, comp39{})
	Add(w, e, comp40{})
	Add(w, e, comp41{})
	Add(w, e, comp42{})
	Add(w, e, comp43{})
	Add(w, e, comp44{})
	Add(w, e, comp45{})
	Add(w, e, comp46{})
	Add(w, e, comp47{})
	Add(w, e, comp48{})
	Add(w, e, comp49{})
	Add(w, e, comp50{})
	Add(w, e, comp51{})
	Add(w, e, comp52{})
	Add(w, e, comp53{})
	Add(w, e, comp54{})
	Add(w, e, comp55{})
	Add(w, e, comp56{})
	Add(w, e, comp57{})
	Add(w, e, comp58{})
	Add(w, e, comp59{})
	Add(w, e, comp60{})
	Add(w, e, comp61{})
	Add(w, e, comp62{})
	Add(w, e, comp63{})
	Add(w, e, comp64{})
}

// 大 ID 实体（存档恢复场景）：CreateEntityWithID 后挂组件，掩码槽按需扩容。
func TestMaskLargeEntityID(t *testing.T) {
	w := NewWorld()
	// 先创建一个小 ID 实体，让掩码先以 stride 1 分配
	small := w.CreateEntity()
	Add(w, small, comp00{})

	big := Entity(100000)
	w.CreateEntityWithID(big)
	Add(w, big, comp64{})
	Add(w, big, comp01{})

	if !Has[comp64](w, big) || !Has[comp01](w, big) {
		t.Fatal("大 ID 实体组件应可挂载")
	}
	w.DestroyEntity(big)
	for _, typ := range w.storageOrder {
		if st := w.storages[typ].(storageLike); st.hasEntity(big) {
			t.Fatalf("存储 %v 销毁后仍持有大 ID 实体", typ)
		}
	}
	if Has[comp64](w, big) {
		t.Fatal("销毁后组件不应残留")
	}
}

// 掩码内存：每个实体固定 maskWords × 8B，随实体 ID 峰值增长。
func TestMaskMemorySizing(t *testing.T) {
	w := NewWorld()
	for i := 0; i < 1000; i++ {
		e := w.CreateEntity()
		Add(w, e, comp00{})
	}
	// 1000 个实体 + ID 从 1 开始 → 掩码槽 ≥ 1001
	if len(w.masks) < 1001 {
		t.Fatalf("掩码长度 = %d, want ≥ 1001", len(w.masks))
	}
	if len(w.masks) != 1001 {
		t.Logf("掩码长度 = %d（按实体 ID 峰值分配，含首槽）", len(w.masks))
	}
}
