package components

import (
	"testing"

	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

func TestActionStateOldPayloadDefaultsInterruptible(t *testing.T) {
	oldPayload, err := pb.Marshal(&game.ActionState{
		ActionId: 1,
		Kind:     game.ActionKind_ACTION_KIND_ATTACK,
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := (actionStateCodec{}).Decode(oldPayload)
	if err != nil {
		t.Fatal(err)
	}
	if state.Uninterruptible || !state.CanInterrupt() {
		t.Fatalf("旧 ActionState 应默认可中断: %+v", state)
	}
}
