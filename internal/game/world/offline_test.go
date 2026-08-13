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

// TestOfflineDeadReconnectReuse：死亡角色重连复用同一实体，不复活、不恢复状态（无"分身"）。
func TestOfflineDeadReconnectReuse(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{HungerRate: 10})
	player := createPlayer(t, eng, pid, "u1")
	for i := 0; i < 115; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)
	if !ecs.Has[components.Dead](wa.sim, player) {
		t.Fatal("预期角色已饿死")
	}

	again := createPlayer(t, eng, pid, "u1")
	if again != player {
		t.Fatalf("死亡角色重连应复用同一实体: %d != %d", again, player)
	}
	if !ecs.Has[components.Dead](wa.sim, player) {
		t.Fatal("复用后不应复活（保持 Dead）")
	}
	if hp := ecs.Get[components.Health](wa.sim, player); hp.Cur != 0 {
		t.Fatalf("不应恢复血量: hp=%d", hp.Cur)
	}
}

// TestReconnectReuseWithoutOffline：旧连接尚未标记 Offline 时重连（竞态窗口），
// 必须复用同一实体，不能产生重复/僵尸实体。
func TestReconnectReuseWithoutOffline(t *testing.T) {
	eng, pid, wa, _ := newM5World(t, WorldConfig{})
	first := createPlayer(t, eng, pid, "u1")
	second := createPlayer(t, eng, pid, "u1") // 不经过断线，模拟 sweeper 竞态
	if second != first {
		t.Fatalf("竞态窗口重连应复用实体：first=%d second=%d", first, second)
	}
	n := 0
	ecs.Query[components.Player](wa.sim, func(e ecs.Entity, p *components.Player) {
		if p.UID == "u1" {
			n++
		}
	})
	if n != 1 {
		t.Fatalf("u1 实体数 = %d, want 1", n)
	}
}
