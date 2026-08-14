package ecs

import (
	"reflect"
	"testing"
)

// 探针组件：记录 OnAdd/OnRemove 触发，并验证销毁时其他组件仍可读。
type lifeA struct{}
type lifeB struct{}

var (
	lifeAdds    []string
	lifeRemoves []string
	lifeBroken  []string
)

func (lifeA) OnAdd(w *World, e Entity)    { lifeAdds = append(lifeAdds, "a") }
func (lifeA) OnRemove(w *World, e Entity) { lifeRemoves = append(lifeRemoves, "a") }
func (lifeB) OnAdd(w *World, e Entity)    { lifeAdds = append(lifeAdds, "b") }
func (lifeB) OnRemove(w *World, e Entity) {
	if !Has[lifeA](w, e) {
		lifeBroken = append(lifeBroken, "b-without-a")
	}
	lifeRemoves = append(lifeRemoves, "b")
}

// TryResourceOf 测试用的资源与接口。
type resA struct{ n int }

type resIface interface{ Value() int }

func (r *resA) Value() int { return r.n }

type otherIface interface{ Nope() }

func resetLifeProbes() {
	lifeAdds = nil
	lifeRemoves = nil
	lifeBroken = nil
}

// Add/Remove 触发生命周期钩子。
func TestLifecycleAddRemove(t *testing.T) {
	resetLifeProbes()
	w := NewWorld()
	e := w.CreateEntity()

	Add(w, e, lifeA{})
	Add(w, e, lifeB{})
	if len(lifeAdds) != 2 || lifeAdds[0] != "a" || lifeAdds[1] != "b" {
		t.Fatalf("OnAdd 顺序 = %v, want [a b]", lifeAdds)
	}

	Remove[lifeA](w, e)
	if len(lifeRemoves) != 1 || lifeRemoves[0] != "a" {
		t.Fatalf("OnRemove = %v, want [a]", lifeRemoves)
	}
	Remove[lifeB](w, e)
	if len(lifeRemoves) != 2 {
		t.Fatalf("OnRemove = %v, want [a b]", lifeRemoves)
	}
}

// DestroyEntity 先触发所有组件的 OnRemove（此时兄弟组件仍完整），再批量清理。
func TestLifecycleDestroy(t *testing.T) {
	resetLifeProbes()
	w := NewWorld()
	e := w.CreateEntity()
	Add(w, e, lifeA{})
	Add(w, e, lifeB{})

	w.DestroyEntity(e)
	if len(lifeRemoves) != 2 || lifeRemoves[0] != "a" || lifeRemoves[1] != "b" {
		t.Fatalf("销毁 OnRemove = %v, want [a b]", lifeRemoves)
	}
	if len(lifeBroken) != 0 {
		t.Fatalf("OnRemove 应看到兄弟组件: %v", lifeBroken)
	}
}

// 未实现接口的组件不受影响。
func TestLifecycleOnlyWhenImplemented(t *testing.T) {
	resetLifeProbes()
	w := NewWorld()
	e := w.CreateEntity()
	type plain struct{}
	Add(w, e, plain{})
	w.DestroyEntity(e)
	if len(lifeAdds) != 0 || len(lifeRemoves) != 0 {
		t.Fatalf("plain 组件不应触发钩子: adds=%v removes=%v", lifeAdds, lifeRemoves)
	}
}

// 掩码：销毁后复用实体 ID，旧组件的掩码位与存储都应清空。
func TestDestroyMaskReuse(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	Add(w, e, lifeA{})
	w.DestroyEntity(e)

	e2 := w.CreateEntity()
	if e2 != e {
		t.Fatalf("销毁后应复用 ID: got %d, want %d", e2, e)
	}
	if Has[lifeA](w, e2) {
		t.Fatal("复用 ID 不应残留旧组件")
	}
	Add(w, e2, lifeB{})
	w.DestroyEntity(e2)
	if w.IsAlive(e2) {
		t.Fatal("销毁后实体不应存活")
	}
}

// TryResourceOf 按接口找到已注入资源。
func TestTryResourceOf(t *testing.T) {
	w := NewWorld()

	w.AddResource(&resA{n: 7})
	v, ok := TryResourceOf[resIface](w)
	if !ok || v.Value() != 7 {
		t.Fatalf("TryResourceOf = %v/%v, want 7/true", v, ok)
	}
	if _, ok := TryResourceOf[otherIface](w); ok {
		t.Fatal("未实现接口的资源不应被找到")
	}
	// 校验：资源类型指针确实实现了接口
	if !reflect.TypeOf(&resA{}).Implements(reflect.TypeOf((*resIface)(nil)).Elem()) {
		t.Fatal("测试资源应实现接口")
	}
}
