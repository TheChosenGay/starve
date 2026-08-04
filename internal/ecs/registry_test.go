package ecs

import (
	"reflect"
	"testing"
)

func TestComponentRegistryName(t *testing.T) {
	w := NewWorld()
	RegisterComponent[hp](w, "HealthPoint")
	e := w.CreateEntity()
	Add(w, e, hp{})
	if ComponentIDOf[hp](w) != "HealthPoint" {
		t.Fatalf("name = %q", ComponentIDOf[hp](w))
	}
	w.DrainDirtySorted() // 清掉 Add 的标记
	Set(w, e, hp{})
	entries := w.DrainDirtySorted()
	if len(entries) != 1 || entries[0].Comps[0] != "HealthPoint" {
		t.Fatalf("dirty comp = %v", entries)
	}
}

func TestRegisterComponentAfterUsePanics(t *testing.T) {
	w := NewWorld()
	e := w.CreateEntity()
	Add(w, e, hp{})
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	RegisterComponent[hp](w, "Renamed")
}

type hpCodec struct{}

func (hpCodec) Encode(v hp) ([]byte, error) { return nil, nil }
func (hpCodec) Decode(b []byte) (hp, error) { return hp{}, nil }

func TestRegisterComponentWithCodec(t *testing.T) {
	w := NewWorld()
	RegisterComponent[hp](w, "HealthPoint", hpCodec{})
	meta, ok := w.Registry().Meta(reflect.TypeOf(hp{}))
	if !ok || meta.Name != "HealthPoint" || meta.Codec == nil {
		t.Fatalf("meta = %+v, ok = %v", meta, ok)
	}
	// 惰性登记的类型也能被 Types() 列出（按名称排序）
	w.CreateEntity()
	ts := w.Registry().Types()
	if len(ts) != 1 || w.Registry().Name(ts[0]) != "HealthPoint" {
		t.Fatalf("types = %v", ts)
	}
}
