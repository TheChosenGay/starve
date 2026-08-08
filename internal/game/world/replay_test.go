package world

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	pb "google.golang.org/protobuf/proto"

	game "starve/pkg/proto/game"
)

// TestReplayMatchesSnapshot：一段操作（移动/攻击/采集/断线/重连）后保存，
// 从空世界按指令日志重放，结果应与存档快照完全一致（确定性验收）。
func TestReplayMatchesSnapshot(t *testing.T) {
	resPath := filepath.Join(t.TempDir(), "resources.json")
	if err := os.WriteFile(resPath, []byte(`[{"kind":"berry","x":0,"y":1,"count":3}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := WorldConfig{
		HungerRate:            1,
		AttackDamage:          10,
		OfflineRetentionTicks: 100,
		ResourcesPath:         resPath,
	}

	eng, pid, wa, _ := newM5World(t, cfg)
	u1 := createPlayer(t, eng, pid, "u1")
	u2 := createPlayer(t, eng, pid, "u2")

	// tick 0：u1 采浆果丛(实体1,距离1)；u2 移动后 u1 攻击 u2（距离校验内）；u1 移动
	eng.Send(pid, Command{UID: "u1", Kind: CommandGather, Data: GatherData{Player: u1, Target: 1}})
	eng.Send(pid, Command{UID: "u2", Kind: CommandMove, Data: MoveData{Entity: u2, DX: 1, DY: 0}})
	eng.Send(pid, Command{UID: "u1", Kind: CommandAttack, Data: AttackData{Attacker: u1, Target: u2}})
	eng.Send(pid, Command{UID: "u1", Kind: CommandMove, Data: MoveData{Entity: u1, DX: 2, DY: 3}})
	eng.Send(pid, Tick{})

	// 若干 tick（饥饿/生长推进）
	for i := 0; i < 4; i++ {
		eng.Send(pid, Tick{})
	}

	// u1/u2 断线（离线保留），u1 重连（复用实体），u2 保持离线到存档
	eng.Send(pid, PlayerDisconnect{UID: "u1"})
	eng.Send(pid, PlayerDisconnect{UID: "u2"})
	eng.Send(pid, Tick{})
	reconnect := createPlayer(t, eng, pid, "u1")
	if reconnect != u1 {
		t.Fatalf("重连应复用 u1 实体：%d != %d", reconnect, u1)
	}
	for i := 0; i < 3; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	data := wa.Save()
	var sd game.SaveData
	if err := pb.Unmarshal(data, &sd); err != nil {
		t.Fatal(err)
	}
	if len(sd.Journal) == 0 {
		t.Fatal("存档缺少指令日志")
	}
	var entries []JournalEntry
	if err := json.Unmarshal(sd.Journal, &entries); err != nil {
		t.Fatal(err)
	}

	replayWa := NewWorldActor(cfg)
	replayWa.Replay(entries, int64(sd.Meta.Tick))

	got := FullSnapshot(replayWa.sim)
	if !pb.Equal(got, sd.Snapshot) {
		t.Fatalf("重放结果与存档快照不一致\n--- 存档 ---\n%v\n--- 重放 ---\n%v", sd.Snapshot, got)
	}
}

// TestReplayEmptyJournal：空日志重放（只含资源 seed）应与全新世界一致。
func TestReplayEmptyJournal(t *testing.T) {
	resPath := filepath.Join(t.TempDir(), "resources.json")
	if err := os.WriteFile(resPath, []byte(`[{"kind":"berry","x":0,"y":1,"count":3}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := WorldConfig{ResourcesPath: resPath}
	eng, pid, wa, _ := newM5World(t, cfg)
	for i := 0; i < 5; i++ {
		eng.Send(pid, Tick{})
	}
	syncWorld(t, eng, pid)

	data := wa.Save()
	var sd game.SaveData
	if err := pb.Unmarshal(data, &sd); err != nil {
		t.Fatal(err)
	}
	var entries []JournalEntry
	if err := json.Unmarshal(sd.Journal, &entries); err != nil {
		t.Fatal(err)
	}
	replayWa := NewWorldActor(cfg)
	replayWa.Replay(entries, int64(sd.Meta.Tick))
	if !pb.Equal(FullSnapshot(replayWa.sim), sd.Snapshot) {
		t.Fatal("空日志重放与存档不一致")
	}
}
