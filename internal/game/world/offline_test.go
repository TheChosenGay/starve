package world

import (
	"testing"

	"starve/internal/ecs"
	"starve/internal/game/components"
)

// TestOfflineReconnectReuse：断线挂 Offline → 重连同 UID 复用同一实体并清除标记。
func TestOfflineReconnectReuse(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	first := createPlayer(t, eng, pid, "u1")

	eng.Send(pid, PlayerDisconnect{UID: "u1"})
	syncWorld(t, eng, pid)

	if !ecs.Has[components.Offline](wa.sim, first) {
		t.Fatal("断线后应有 Offline 组件")
	}
	if off := ecs.Get[components.Offline](wa.sim, first); off.SinceTick != 0 {
		t.Fatalf("SinceTick = %d, want 0", off.SinceTick)
	}

	second := createPlayer(t, eng, pid, "u1")
	if second != first {
		t.Fatalf("重连应复用实体：first=%d second=%d", first, second)
	}
	if ecs.Has[components.Offline](wa.sim, first) {
		t.Fatal("重连后 Offline 应被清除")
	}
}

// TestOfflineTimeoutDestroy：超过保留时长后实体被销毁。
func TestOfflineTimeoutDestroy(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{OfflineRetentionTicks: 2})
	player := createPlayer(t, eng, pid, "u1")
	eng.Send(pid, PlayerDisconnect{UID: "u1"})

	// tick 0/1 未到期，tick 2 时 2-0>=2 → 销毁
	for i := 0; i < 3; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	if wa.sim.IsAlive(player) {
		t.Fatal("离线超时后实体应被销毁")
	}
	if _, ok := wa.players[player]; ok {
		t.Fatal("销毁后所有权表应清理")
	}
}

// TestOfflineDeadNoReuse：死亡角色不参与离线保留，重连创建新实体。
func TestOfflineDeadNoReuse(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{HungerRate: 10})
	player := createPlayer(t, eng, pid, "u1")
	for i := 0; i < 115; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	if !ecs.Has[components.Dead](wa.sim, player) {
		t.Fatal("预期角色已饿死")
	}

	eng.Send(pid, PlayerDisconnect{UID: "u1"})
	syncWorld(t, eng, pid)
	if ecs.Has[components.Offline](wa.sim, player) {
		t.Fatal("死亡角色不应挂 Offline")
	}

	again := createPlayer(t, eng, pid, "u1")
	if again == player {
		t.Fatal("死亡角色应新建实体，不复用")
	}
}
