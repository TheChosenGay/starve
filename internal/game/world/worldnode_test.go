package world

import (
	"testing"
)

func TestRoomSavePathPerRoomName(t *testing.T) {
	a := RoomSavePath("/saves", "room-a")
	b := RoomSavePath("/saves", "room-b")
	if a != "/saves/room-a.bin" {
		t.Fatalf("room-a path = %q", a)
	}
	if b != "/saves/room-b.bin" {
		t.Fatalf("room-b path = %q", b)
	}
	if a == b {
		t.Fatal("different rooms must have distinct save paths")
	}
}

func TestNewWorldNodeProducer(t *testing.T) {
	p := NewWorldNode(WorldConfig{}, nil, "/tmp")
	if p == nil {
		t.Fatal("nil producer")
	}
	if p() == nil {
		t.Fatal("producer returned nil node manager")
	}
}
