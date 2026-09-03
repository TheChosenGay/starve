package world

import (
	"context"
	"testing"
	"time"

	"github.com/TheChosenGay/actor"
	"github.com/TheChosenGay/actor/cluster"
	natsserver "github.com/nats-io/nats-server/v2/server"
)

func startEmbeddedNATS(t *testing.T) (*natsserver.Server, string) {
	t.Helper()
	opts := &natsserver.Options{Host: "127.0.0.1", Port: -1}
	srv, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("nats server: %v", err)
	}
	go srv.Start()
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats not ready")
	}
	return srv, srv.ClientURL()
}

func dataNode(t *testing.T, lb *actor.LoopbackTransport, url, nodeID, addr string, kinds ...string) *actor.Engine {
	t.Helper()
	eng := actor.NewEngine(actor.Config{Cluster: actor.ClusterConfig{
		Enable:       true,
		RegistryAddr: "reg",
		NodeAddr:     addr,
		NodeID:       nodeID,
		Transport:    lb,
		Client: cluster.NewNATSClient(actor.ClusterConfig{
			RegistryAddr:      "reg",
			NodeAddr:          addr,
			NodeID:            nodeID,
			Transport:         lb,
			RegisterTimeout:   3 * time.Second,
			HeartbeatInterval: 100 * time.Millisecond,
			LeaseTTL:          5 * time.Second,
		}, url, kinds...),
	}})
	return eng
}

// TestClusterCreateWorldCrossNode：大厅（集群成员）跨节点调用世界节点 NodeManager 创建世界。
func TestClusterCreateWorldCrossNode(t *testing.T) {
	srv, url := startEmbeddedNATS(t)
	defer srv.Shutdown()
	lb := actor.NewLoopbackTransport()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 服务中心。
	regEng := actor.NewEngine(actor.Config{Cluster: actor.ClusterConfig{
		IsRegistry: true, NodeAddr: "reg", NodeID: "reg", Transport: lb,
		Service: cluster.NewNATSServiceCenter(actor.ClusterConfig{
			NodeAddr: "reg", NodeID: "reg", LeaseTTL: 5 * time.Second,
		}, url),
	}})
	if err := regEng.StartCluster(ctx); err != nil {
		t.Fatalf("registry: %v", err)
	}
	defer regEng.Shutdown()

	// 世界节点。
	worldEng := dataNode(t, lb, url, "world", "W:8082", "world")
	if err := worldEng.StartCluster(ctx); err != nil {
		t.Fatalf("world: %v", err)
	}
	defer worldEng.Shutdown()
	nodePID := worldEng.Spawn(func() actor.IActor {
		return NewNodeManager(func(meta RoomMeta) actor.IActor { return &mockRoom{} }, "")
	}, "worldnode", "manager")

	// 大厅。
	lobbyEng := dataNode(t, lb, url, "lobby", "L:8082", "lobby")
	if err := lobbyEng.StartCluster(ctx); err != nil {
		t.Fatalf("lobby: %v", err)
	}
	defer lobbyEng.Shutdown()

	// 重试直到大厅 roster 能看到世界节点（注册有延迟），再跨节点创建世界。
	target := &actor.PID{Address: "W:8082", ID: "worldnode/manager"}
	deadline := time.Now().Add(5 * time.Second)
	var created CreateWorldResp
	for time.Now().Before(deadline) {
		resp := lobbyEng.Request(target, CreateWorld{RoomName: "room-1", AccessToken: "tk1"}, 2*time.Second)
		v, err := resp.Wait()
		if err == nil {
			if c, ok := v.(CreateWorldResp); ok && c.PID != nil {
				created = c
				break
			}
			t.Logf("create resp: %#v (%T)", v, v)
		} else {
			t.Logf("create err: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if created.PID == nil {
		t.Log("create did not succeed; checking NodeManager locally")
		q := worldEng.Request(nodePID, QueryWorlds{}, time.Second)
		if v, err := q.Wait(); err == nil {
			t.Fatalf("cross-node CreateWorld did not succeed; local QueryWorlds = %#v", v)
		}
		t.Fatal("cross-node CreateWorld did not succeed (node manager unreachable locally)")
	}

	// 世界节点上 QueryToken 应命中。
	q := worldEng.Request(nodePID, QueryToken{AccessToken: "tk1"}, 2*time.Second)
	v, err := q.Wait()
	if err != nil {
		t.Fatalf("query token: %v", err)
	}
	qt := v.(QueryTokenResp)
	if !qt.OK || qt.RoomName != "room-1" {
		t.Fatalf("query token = %#v", qt)
	}
}
